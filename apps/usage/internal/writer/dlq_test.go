package writer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/kafka"
)

type fakeDLQ struct {
	mu       sync.Mutex
	got      []kafka.Poison
	fail     atomic.Bool
	failures atomic.Int64
}

func (d *fakeDLQ) Send(_ context.Context, poison []kafka.Poison) error {
	if d.fail.Load() {
		d.failures.Add(1)
		return errors.New("dlq: not confirmed")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.got = append(d.got, poison...)
	return nil
}

func (d *fakeDLQ) values() [][]byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]byte, len(d.got))
	for i, p := range d.got {
		out[i] = p.Record.Value
	}
	return out
}

func waitForDLQ(t *testing.T, d *fakeDLQ, want int, within time.Duration) [][]byte {
	t.Helper()
	deadline := time.Now().Add(within)
	for len(d.values()) < want && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	got := d.values()
	if len(got) != want {
		t.Fatalf("%d records in the DLQ, want %d", len(got), want)
	}
	return got
}

func (p *pipeline) produceRaw(key string, values ...[]byte) {
	p.t.Helper()
	client, err := kgo.NewClient(kgo.SeedBrokers(p.brokers...), kgo.DefaultProduceTopic(topic))
	if err != nil {
		p.t.Fatal(err)
	}
	defer client.Close()
	recs := make([]*kgo.Record, len(values))
	for i, v := range values {
		recs[i] = &kgo.Record{Key: []byte(key), Value: v}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.ProduceSync(ctx, recs...).FirstErr(); err != nil {
		p.t.Fatal(err)
	}
}

func rawEvent(t *testing.T, e core.Event, schemaVersion int) []byte {
	t.Helper()
	v, err := kafka.Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	if schemaVersion != core.SchemaVersion {
		v = bytes.Replace(v, fmt.Appendf(nil, `"schema_version":%d`, core.SchemaVersion), fmt.Appendf(nil, `"schema_version":%d`, schemaVersion), 1)
	}
	return v
}

func containsAll(t *testing.T, got [][]byte, want ...[]byte) {
	t.Helper()
	for _, w := range want {
		found := false
		for _, g := range got {
			if bytes.Equal(g, w) {
				found = true
			}
		}
		if !found {
			t.Fatalf("DLQ is missing %q byte for byte; got %q", w, got)
		}
	}
}

// With the old skip this test fails (T-12 canary).
// Verifies: usage-writer AC-1; T-12
func TestUndecodableRecordGoesToDLQ(t *testing.T) {
	p := newPipeline(t)
	dlq := &fakeDLQ{}
	p.startWriter(p.st, dlq, nil)

	noTenant := newEvent("tenant-a")
	noTenant.TenantID = ""
	poison := [][]byte{
		[]byte(`{"schema_version":1,"event_id":`),
		rawEvent(t, noTenant, core.SchemaVersion),
		rawEvent(t, newEvent("tenant-a"), core.SchemaVersion+98),
	}
	p.produceRaw("tenant-a", poison...)
	after := []core.Event{newEvent("tenant-a"), newEvent("tenant-b")}
	if code := p.send(after...); code != http.StatusAccepted {
		t.Fatalf("send: %d", code)
	}

	p.waitForCount(after, len(after), 20*time.Second)
	containsAll(t, waitForDLQ(t, dlq, len(poison), 10*time.Second), poison...)
}

// Verifies: usage-writer AC-2
func TestInvalidEventGoesToDLQBeforeStore(t *testing.T) {
	p := newPipeline(t)
	dlq := &fakeDLQ{}
	seen := &recordingStore{next: p.st}
	p.startWriter(seen, dlq, nil)

	bad := newEvent("tenant-a")
	bad.Quantity = 0
	// Older than UsageWriteAPI's 7-day window, still stored.
	late := newEvent("tenant-a")
	late.Time = time.Now().Add(-10 * 24 * time.Hour).UTC()
	good := newEvent("tenant-b")
	p.produceRaw("tenant-a", rawEvent(t, bad, core.SchemaVersion), rawEvent(t, late, core.SchemaVersion), rawEvent(t, good, core.SchemaVersion))

	p.waitForCount([]core.Event{late, good}, 2, 20*time.Second)
	containsAll(t, waitForDLQ(t, dlq, 1, 10*time.Second), rawEvent(t, bad, core.SchemaVersion))
	if seen.saw(bad.ID) {
		t.Fatal("the invalid event reached the database")
	}
}

// Verifies: usage-writer AC-5
func TestFailedDLQWriteCommitsNoOffset(t *testing.T) {
	p := newPipeline(t)
	dlq := &fakeDLQ{}
	dlq.fail.Store(true)
	_, stop, _ := p.startWriter(p.st, dlq, nil)

	poison := []byte(`not json`)
	good := newEvent("tenant-a")
	p.produceRaw("tenant-a", poison, rawEvent(t, good, core.SchemaVersion))
	deadline := time.Now().Add(10 * time.Second)
	for dlq.failures.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if dlq.failures.Load() < 2 {
		t.Fatal("the writer did not retry the DLQ write; the test proves nothing")
	}
	stop()

	dlq.fail.Store(false)
	p.startWriter(p.st, dlq, nil)
	containsAll(t, waitForDLQ(t, dlq, 1, 20*time.Second), poison)
	p.waitForCount([]core.Event{good}, 1, 20*time.Second)
}

type recordingStore struct {
	next core.EventWriter
	mu   sync.Mutex
	ids  map[string]bool
}

func (s *recordingStore) Write(ctx context.Context, events []core.Event) ([]core.Event, error) {
	s.mu.Lock()
	if s.ids == nil {
		s.ids = map[string]bool{}
	}
	for _, e := range events {
		s.ids[e.ID] = true
	}
	s.mu.Unlock()
	return s.next.Write(ctx, events)
}

func (s *recordingStore) saw(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ids[id]
}

// NUL in attributes: valid JSON, invalid jsonb (22P05). Without the split the batch retries forever.
// Verifies: usage-writer AC-3; T-01; T-12
func TestDatabasePoisonIsIsolatedByBisection(t *testing.T) {
	p := newPipeline(t)
	dlq := &fakeDLQ{}

	bad := newEvent("tenant-a")
	bad.Attributes = []byte(`{"path":"/a\u0000b"}`)
	var good []core.Event
	values := [][]byte{}
	for i := range 7 {
		e := newEvent([]string{"tenant-a", "tenant-b"}[i%2])
		good = append(good, e)
		values = append(values, rawEvent(t, e, core.SchemaVersion))
		if i == 3 {
			values = append(values, rawEvent(t, bad, core.SchemaVersion))
		}
	}
	// One key, so all records arrive in one fetch.
	p.produceRaw("mixed", values...)
	p.startWriter(p.st, dlq, nil)

	p.waitForCount(good, len(good), 20*time.Second)
	containsAll(t, waitForDLQ(t, dlq, 1, 10*time.Second), rawEvent(t, bad, core.SchemaVersion))
	if n := p.db.Count(t, []string{bad.ID}); n != 0 {
		t.Fatalf("the bad event was stored %d times", n)
	}
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		var own []core.Event
		for _, e := range good {
			if e.TenantID == tenant {
				own = append(own, e)
			}
		}
		if n := p.db.CountTenant(t, tenant, ids(own)); n != len(own) {
			t.Fatalf("%s: %d of its %d events are stored under it", tenant, n, len(own))
		}
	}
}
