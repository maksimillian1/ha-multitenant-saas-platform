package writeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/event"
)

type fakeProducer struct {
	err   error
	calls int
	got   []event.Event
}

func (f *fakeProducer) Produce(_ context.Context, events []event.Event) error {
	f.calls++
	f.got = append(f.got, events...)
	return f.err
}

func (f *fakeProducer) Ping(context.Context) error { return f.err }

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func ev(tenant string) map[string]any {
	return map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "event_time": time.Now().UTC().Format(time.RFC3339Nano),
		"tenant_id": tenant, "metric": "api_calls", "quantity": 1,
		"attributes": map[string]any{"method": "GET", "path": "/items", "status": 200},
	}
}

func post(t *testing.T, h http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case string:
		raw = []byte(b)
	default:
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader(raw)))
	return w
}

// AC-5: 202 only after the producer confirmed the whole batch.
func TestAcceptsOnlyAfterKafkaConfirms(t *testing.T) {
	p := &fakeProducer{}
	w := post(t, New(p, quiet), map[string]any{"events": []any{ev("a"), ev("b")}})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if p.calls != 1 || len(p.got) != 2 || p.got[0].TenantID != "a" || p.got[1].TenantID != "b" {
		t.Fatalf("producer got %d calls, events %+v", p.calls, p.got)
	}
}

// AC-5: when Kafka does not confirm, the answer is 503.
func TestKafkaFailureIs503(t *testing.T) {
	p := &fakeProducer{err: errors.New("NOT_ENOUGH_REPLICAS")}
	if w := post(t, New(p, quiet), map[string]any{"events": []any{ev("a")}}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", w.Code)
	}

	// The real producer against a broker that does not exist fails within its
	// delivery timeout instead of hanging.
	k, err := NewKafka([]string{"127.0.0.1:1"}, "usage-events", kgo.RecordDeliveryTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if w := post(t, New(k, quiet), map[string]any{"events": []any{ev("a")}}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unreachable Kafka: status %d, want 503", w.Code)
	}
}

// AC-6: an invalid batch is rejected with 400 and nothing reaches Kafka.
func TestInvalidBatchIsRejectedWhole(t *testing.T) {
	bad := ev("a")
	delete(bad, "event_id")
	old := ev("a")
	old["event_time"] = time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339)
	for name, body := range map[string]any{
		"malformed json":   `{"events": [`,
		"no events":        map[string]any{"events": []any{}},
		"one bad event":    map[string]any{"events": []any{ev("a"), bad}},
		"too old":          map[string]any{"events": []any{old}},
		"unknown metric":   map[string]any{"events": []any{func() map[string]any { e := ev("a"); e["metric"] = "x"; return e }()}},
		"not a uuid":       map[string]any{"events": []any{func() map[string]any { e := ev("a"); e["event_id"] = "1"; return e }()}},
		"quantity missing": map[string]any{"events": []any{func() map[string]any { e := ev("a"); delete(e, "quantity"); return e }()}},
	} {
		p := &fakeProducer{}
		w := post(t, New(p, quiet), body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, w.Code)
		}
		if p.calls != 0 {
			t.Errorf("%s: producer called", name)
		}
	}
	p := &fakeProducer{}
	if w := post(t, New(p, quiet), `{"events":[`+strings.Repeat(" ", maxBody)+`]}`); w.Code != http.StatusRequestEntityTooLarge || p.calls != 0 {
		t.Errorf("oversized body: status %d, producer calls %d", w.Code, p.calls)
	}
}

// AC-5 (integration): the producer waits for all in-sync replicas and is idempotent.
func TestProducerRequiresAllISRAndIdempotence(t *testing.T) {
	k, err := NewKafka([]string{"127.0.0.1:1"}, "usage-events")
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if got := k.client.OptValue(kgo.RequiredAcks); got != kgo.AllISRAcks() {
		t.Fatalf("RequiredAcks = %v, want all ISR", got)
	}
	if got := k.client.OptValue(kgo.DisableIdempotentWrite); got != false {
		t.Fatalf("DisableIdempotentWrite = %v, want false", got)
	}
}

// A 202 means the events are in the topic, keyed by tenant_id.
func TestAcceptedEventsAreInKafka(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(3), kfake.SeedTopics(3, "usage-events"))
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	k, err := NewKafka(cluster.ListenAddrs(), "usage-events")
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()

	a, b := ev("tenant-a"), ev("tenant-b")
	if w := post(t, New(k, quiet), map[string]any{"events": []any{a, b}}); w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}

	consumer, err := kgo.NewClient(kgo.SeedBrokers(cluster.ListenAddrs()...), kgo.ConsumeTopics("usage-events"),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	seen := map[string]string{}
	for len(seen) < 2 && ctx.Err() == nil {
		consumer.PollFetches(ctx).EachRecord(func(r *kgo.Record) {
			var e event.Event
			if err := json.Unmarshal(r.Value, &e); err != nil {
				t.Fatal(err)
			}
			if string(r.Key) != e.TenantID {
				t.Errorf("record key %q, want tenant %q", r.Key, e.TenantID)
			}
			seen[e.ID] = e.TenantID
		})
	}
	if seen[a["event_id"].(string)] != "tenant-a" || seen[b["event_id"].(string)] != "tenant-b" {
		t.Fatalf("records in Kafka: %v", seen)
	}
}
