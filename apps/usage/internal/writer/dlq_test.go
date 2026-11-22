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

// fakeDLQ keeps what the writer sends to the DLQ and can fail like a DLQ
// topic that Kafka does not confirm.
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

// waitForDLQ waits until exactly want records are in the DLQ and checks the
// count stays there.
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

// produceRaw writes values to the usage topic as they are, bypassing
// UsageWriteAPI, the way a buggy or outdated producer would.
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

// A record that does not decode goes to the DLQ with its bytes unchanged, and
// the records after it on the partition are stored. With the old skip the
// record is not in the DLQ and this test fails (T-12 canary).
//
// Verifies: usage-writer AC-1; T-12
func TestUndecodableRecordGoesToDLQ(t *testing.T) {
	p := newPipeline(t)
	dlq := &fakeDLQ{}
	p.startWriter(p.st, dlq, nil)

	noTenant := newEvent("tenant-a")
	noTenant.TenantID = ""
	poison := [][]byte{
		[]byte(`{"schema_version":1,"event_id":`),                // bad JSON
		rawEvent(t, noTenant, core.SchemaVersion),                // tenant_id missing
		rawEvent(t, newEvent("tenant-a"), core.SchemaVersion+98), // newer than this writer
	}
	p.produceRaw("tenant-a", poison...)
	after := []core.Event{newEvent("tenant-a"), newEvent("tenant-b")}
	if code := p.send(after...); code != http.StatusAccepted {
		t.Fatalf("send: %d", code)
	}

	p.waitForCount(after, len(after), 20*time.Second)
	containsAll(t, waitForDLQ(t, dlq, len(poison), 10*time.Second), poison...)
}

// A record that decodes but breaks the contract (quantity 0) goes to the DLQ
// before the database; the rest of the same fetch is stored.
//
// Verifies: usage-writer AC-2
func TestInvalidEventGoesToDLQBeforeStore(t *testing.T) {
	p := newPipeline(t)
	dlq := &fakeDLQ{}
	seen := &recordingStore{next: p.st}
	p.startWriter(seen, dlq, nil)

	bad := newEvent("tenant-a")
	bad.Quantity = 0
	// Older than UsageWriteAPI's 7-day window: the writer checks the contract
	// without the window, so a late event is still stored.
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

// While the DLQ cannot be written, no offset is committed: after a restart
// with a working DLQ the poison record is there and the good one stored once.
//
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

// recordingStore remembers every event ID the writer asked it to store.
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
