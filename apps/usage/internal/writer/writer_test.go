package writer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/event"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/store"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/testpg"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/writeapi"
)

// These tests run the pipeline in one process: UsageWriteAPI → Kafka (kfake,
// 3 brokers) → Usage Writer → PostgreSQL (TEST_DATABASE_URL).

const topic = "usage-events"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type pipeline struct {
	t       *testing.T
	db      testpg.DB
	st      *store.Postgres
	brokers []string
	api     http.Handler
	group   string
}

func newPipeline(t *testing.T) *pipeline {
	t.Helper()
	db := testpg.New(t)
	cluster, err := kfake.NewCluster(kfake.NumBrokers(3), kfake.SeedTopics(3, topic))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cluster.Close)
	k, err := writeapi.NewKafka(cluster.ListenAddrs(), topic)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Close)
	st, err := store.Open(context.Background(), db.WriterDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return &pipeline{t: t, db: db, st: st, brokers: cluster.ListenAddrs(), api: writeapi.New(k, quiet), group: "usage-writer"}
}

func newEvent(tenant string) event.Event {
	return event.Event{
		ID: uuid.Must(uuid.NewV7()).String(), Time: time.Now().UTC(), TenantID: tenant,
		Metric: "api_calls", Quantity: 1, Attributes: json.RawMessage(`{"method":"GET","path":"/items","status":200}`),
	}
}

// send posts events to UsageWriteAPI and returns the status code.
func (p *pipeline) send(events ...event.Event) int {
	p.t.Helper()
	raw, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		p.t.Fatal(err)
	}
	w := httptest.NewRecorder()
	p.api.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader(raw)))
	return w.Code
}

// startWriter runs a Usage Writer on st until the returned stop is called or
// the writer returns on its own.
func (p *pipeline) startWriter(st store.Writer, afterStore func() error) (w *Writer, stop func(), done <-chan error) {
	p.t.Helper()
	client, err := NewClient(p.brokers, topic, p.group)
	if err != nil {
		p.t.Fatal(err)
	}
	w = New(client, st, quiet)
	w.afterStore = afterStore
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		ch <- w.Run(ctx)
		client.Close()
		close(finished)
	}()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); <-finished }) }
	p.t.Cleanup(stop)
	return w, stop, ch
}

func ids(events []event.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.ID
	}
	return out
}

// waitForCount waits until exactly want rows of these events are stored, then
// checks that the count stays there.
func (p *pipeline) waitForCount(events []event.Event, want int, within time.Duration) {
	p.t.Helper()
	deadline := time.Now().Add(within)
	for {
		n := p.db.Count(p.t, ids(events))
		if n == want {
			break
		}
		if n > want || time.Now().After(deadline) {
			p.t.Fatalf("%d rows stored, want %d", n, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if n := p.db.Count(p.t, ids(events)); n != want {
		p.t.Fatalf("%d rows after settling, want %d", n, want)
	}
}

// AC-9 / QS-06: the same event sent 3 times is stored once.
func TestSameEventThreeTimesIsStoredOnce(t *testing.T) {
	p := newPipeline(t)
	w, _, _ := p.startWriter(p.st, nil)
	e := newEvent("tenant-a")
	for i := 0; i < 3; i++ {
		if code := p.send(e); code != http.StatusAccepted {
			t.Fatalf("send %d: status %d", i, code)
		}
	}
	p.waitForCount([]event.Event{e}, 1, 10*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for w.Skipped.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if w.Stored.Load() != 1 || w.Skipped.Load() != 2 {
		t.Fatalf("stored %d, skipped %d; want 1 and 2", w.Stored.Load(), w.Skipped.Load())
	}
}

// AC-10 / QS-06: a writer that dies after the database commit and before the
// offset commit leaves the batch uncommitted; the next writer stores it again,
// and the dedup key keeps one row per event.
func TestCrashBeforeOffsetCommitStoresOnce(t *testing.T) {
	p := newPipeline(t)
	var events []event.Event
	for i := 0; i < 50; i++ {
		e := newEvent([]string{"tenant-a", "tenant-b"}[i%2])
		events = append(events, e)
	}
	if code := p.send(events...); code != http.StatusAccepted {
		t.Fatalf("status %d", code)
	}

	crash := errors.New("simulated crash before the offset commit")
	_, _, done := p.startWriter(p.st, func() error { return crash })
	select {
	case err := <-done:
		if !errors.Is(err, crash) {
			t.Fatalf("first writer returned %v, want the simulated crash", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("first writer did not reach the offset commit")
	}
	if n := p.db.Count(t, ids(events)); n == 0 {
		t.Fatal("the first writer stored nothing before its crash; the test proves nothing")
	}

	w, _, _ := p.startWriter(p.st, nil)
	p.waitForCount(events, len(events), 15*time.Second)
	deadline := time.Now().Add(10 * time.Second)
	for w.Skipped.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if w.Skipped.Load() == 0 {
		t.Fatal("the second writer saw no replayed events; the crash did not leave the batch uncommitted")
	}
}

// failingStore fails every write until the outage ends, like a database that
// cannot be reached.
type failingStore struct {
	next     store.Writer
	down     atomic.Bool
	failures atomic.Int64
}

func (f *failingStore) Write(ctx context.Context, events []event.Event) ([]event.Event, error) {
	if f.down.Load() {
		f.failures.Add(1)
		return nil, errors.New("connection refused")
	}
	return f.next.Write(ctx, events)
}

// AC-13 / QS-09 (in-process): while the database is down, UsageWriteAPI keeps
// accepting events; once it is back, every accepted event is stored once.
func TestDatabaseOutageLosesNothing(t *testing.T) {
	p := newPipeline(t)
	fs := &failingStore{next: p.st}
	fs.down.Store(true)
	p.startWriter(fs, nil)

	var accepted []event.Event
	for i := 0; i < 40; i++ {
		e := newEvent([]string{"tenant-a", "tenant-b"}[i%2])
		if code := p.send(e); code != http.StatusAccepted {
			t.Fatalf("event %d during the outage: status %d, want 202", i, code)
		}
		accepted = append(accepted, e)
		time.Sleep(25 * time.Millisecond)
	}
	if fs.failures.Load() == 0 {
		t.Fatal("the writer never tried the database during the outage; the test proves nothing")
	}
	if n := p.db.Count(t, ids(accepted)); n != 0 {
		t.Fatalf("%d rows stored during the outage", n)
	}

	fs.down.Store(false)
	p.waitForCount(accepted, len(accepted), 20*time.Second)
}
