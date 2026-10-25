package usage

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func event() Event {
	return APICall("tenant-a", time.Now(), http.MethodGet, "/items", 200, time.Millisecond, 0, 10)
}

func metric(r *Reporter, name string) string {
	var b strings.Builder
	r.WriteMetrics(&b)
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.HasPrefix(line, name+" ") {
			return strings.TrimPrefix(line, name+" ")
		}
	}
	return ""
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// AC-4: at the limit of unsent events, Report drops new events at once and
// counts them on the metrics endpoint.
func TestDropsBeyondLimitWithoutBlocking(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // UsageWriteAPI hangs
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	defer close(release)

	r := New(Config{URL: srv.URL, Limit: 2, Client: &http.Client{Timeout: time.Minute}}, quiet)
	start := time.Now()
	for i := 0; i < 5; i++ {
		r.Report(event())
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("Report blocked for %s", d)
	}
	if got := metric(r, "usage_events_dropped_total"); got != "3" {
		t.Fatalf("usage_events_dropped_total = %s, want 3", got)
	}
	if got := metric(r, "usage_events_pending"); got != "2" {
		t.Fatalf("usage_events_pending = %s, want 2", got)
	}
}

// AC-3: while UsageWriteAPI fails, the event is retried with the same
// event_id and event_time until it is accepted, exactly once.
func TestRetriesUntilDelivered(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string][]map[string]any
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string][]map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		if calls.Add(1) <= 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	r := New(Config{URL: srv.URL, Limit: 10}, quiet)
	r.backoff = func(int) time.Duration { return time.Millisecond }
	e := event()
	r.Report(e)
	waitFor(t, "delivery", func() bool { return metric(r, "usage_events_sent_total") == "1" })
	r.Close(context.Background())

	if calls.Load() != 4 {
		t.Fatalf("%d requests, want 4 (3 failures, 1 success)", calls.Load())
	}
	for _, b := range bodies {
		got := b["events"][0]
		if got["event_id"] != e.ID || got["event_time"] != e.Time.Format(time.RFC3339Nano) {
			t.Fatalf("retry changed the identity: %v, want %s at %s", got, e.ID, e.Time.Format(time.RFC3339Nano))
		}
	}
	if got := metric(r, "usage_events_failed_attempts_total"); got != "3" {
		t.Fatalf("usage_events_failed_attempts_total = %s, want 3", got)
	}
	if got := metric(r, "usage_events_pending"); got != "0" {
		t.Fatalf("usage_events_pending = %s, want 0", got)
	}
}

// An event UsageWriteAPI rejects with 400 is not retried.
func TestRejectedEventIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	r := New(Config{URL: srv.URL}, quiet)
	r.Report(event())
	waitFor(t, "rejection", func() bool { return metric(r, "usage_events_rejected_total") == "1" })
	r.Close(context.Background())
	if calls.Load() != 1 {
		t.Fatalf("%d requests, want 1", calls.Load())
	}
}

// An event that is still failing when its retry time is over is given up and counted.
func TestEventIsGivenUpAfterRetryFor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	r := New(Config{URL: srv.URL, RetryFor: 20 * time.Millisecond}, quiet)
	r.backoff = func(int) time.Duration { return time.Millisecond }
	r.Report(event())
	waitFor(t, "give up", func() bool { return metric(r, "usage_events_expired_total") == "1" })
}
