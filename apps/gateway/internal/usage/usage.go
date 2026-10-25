// Package usage reports every API call as a usage event to UsageWriteAPI
// (docs/contracts/usage-events.md, HAMSP-26). Each event is one asynchronous
// request with retries, so the client's response never waits for usage. At
// most Limit events may be unsent at a time; beyond that new events are
// dropped and counted, so an outage of UsageWriteAPI cannot exhaust the
// Gateway's memory.
package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type Event struct {
	ID         string         `json:"event_id"`
	Time       time.Time      `json:"event_time"`
	TenantID   string         `json:"tenant_id"`
	Metric     string         `json:"metric"`
	Quantity   int64          `json:"quantity"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// APICall is the usage event of one request served for a tenant.
func APICall(tenant string, start time.Time, method, path string, status int, duration time.Duration, reqBytes, respBytes int64) Event {
	return Event{
		ID: uuid.Must(uuid.NewV7()).String(), Time: start.UTC(), TenantID: tenant, Metric: "api_calls", Quantity: 1,
		Attributes: map[string]any{
			"method": method, "path": path, "status": status, "duration_ms": duration.Milliseconds(),
			"request_bytes": reqBytes, "response_bytes": respBytes,
		},
	}
}

type Config struct {
	URL   string // UsageWriteAPI, e.g. http://usage-writeapi.usage.svc:8080/v1/events
	Limit int    // unsent events (in flight or retrying) at most
	// RetryFor is how long one event is retried before it is given up.
	RetryFor time.Duration
	Client   *http.Client
}

type Reporter struct {
	cfg     Config
	slots   chan struct{}
	log     *slog.Logger
	stop    chan struct{}
	stopped sync.Once
	wg      sync.WaitGroup
	backoff func(attempt int) time.Duration

	sent, dropped, rejected, expired, failedAttempts atomic.Int64
}

func New(cfg Config, log *slog.Logger) *Reporter {
	if cfg.Limit <= 0 {
		cfg.Limit = 10000
	}
	if cfg.RetryFor <= 0 {
		cfg.RetryFor = 15 * time.Minute
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 3 * time.Second}
	}
	return &Reporter{
		cfg: cfg, slots: make(chan struct{}, cfg.Limit), log: log, stop: make(chan struct{}),
		backoff: func(attempt int) time.Duration {
			return min(100*time.Millisecond<<min(attempt, 6), 5*time.Second)
		},
	}
}

// Report queues the event and returns at once. It never blocks the request.
func (r *Reporter) Report(e Event) {
	select {
	case r.slots <- struct{}{}:
	default:
		r.dropped.Add(1)
		return
	}
	r.wg.Add(1)
	go func() {
		defer func() { <-r.slots; r.wg.Done() }()
		r.deliver(e)
	}()
}

func (r *Reporter) deliver(e Event) {
	body, err := json.Marshal(map[string]any{"events": []Event{e}})
	if err != nil {
		r.rejected.Add(1)
		r.log.Error("usage event not encodable", "err", err)
		return
	}
	deadline := time.Now().Add(r.cfg.RetryFor)
	for attempt := 0; ; attempt++ {
		status, err := r.post(body)
		switch {
		case err == nil && status == http.StatusAccepted:
			r.sent.Add(1)
			return
		case err == nil && status >= 400 && status < 500:
			// The event breaks the contract; sending it again changes nothing.
			r.rejected.Add(1)
			r.log.Error("usage event rejected", "status", status, "event_id", e.ID)
			return
		}
		r.failedAttempts.Add(1)
		if time.Now().After(deadline) {
			r.expired.Add(1)
			r.log.Error("usage event given up", "event_id", e.ID, "status", status, "err", err)
			return
		}
		select {
		case <-r.stop:
			r.expired.Add(1)
			return
		case <-time.After(r.backoff(attempt)):
		}
	}
}

func (r *Reporter) post(body []byte) (int, error) {
	resp, err := r.cfg.Client.Post(r.cfg.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

// Close stops retrying and waits until the events in flight are done or ctx ends.
func (r *Reporter) Close(ctx context.Context) {
	r.stopped.Do(func() { close(r.stop) })
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// WriteMetrics writes the counters in the Prometheus text format.
func (r *Reporter) WriteMetrics(w io.Writer) {
	for _, m := range []struct {
		name, help, kind string
		value            int64
	}{
		{"usage_events_sent_total", "Usage events accepted by UsageWriteAPI.", "counter", r.sent.Load()},
		{"usage_events_dropped_total", "Usage events dropped because the limit of unsent events was reached.", "counter", r.dropped.Load()},
		{"usage_events_rejected_total", "Usage events UsageWriteAPI rejected as invalid.", "counter", r.rejected.Load()},
		{"usage_events_expired_total", "Usage events given up after retrying or at shutdown.", "counter", r.expired.Load()},
		{"usage_events_failed_attempts_total", "Failed attempts to send a usage event, each followed by a retry.", "counter", r.failedAttempts.Load()},
		{"usage_events_pending", "Usage events in flight or waiting for a retry.", "gauge", int64(len(r.slots))},
	} {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %d\n", m.name, m.help, m.name, m.kind, m.name, m.value)
	}
}
