// Package writeapi is UsageWriteAPI (docs/contracts/usage-events.md): it
// validates a batch of usage events and answers 202 only after Kafka confirmed
// every event (ADR-0001 item 4, ADR-0005).
package writeapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/event"
)

const maxBody = 1 << 20

// Producer writes events to Kafka and returns only once all of them are
// confirmed, or with an error.
type Producer interface {
	Produce(ctx context.Context, events []event.Event) error
	Ping(ctx context.Context) error
}

type handler struct {
	producer Producer
	log      *slog.Logger
	now      func() time.Time
	timeout  time.Duration
}

func New(p Producer, log *slog.Logger) http.Handler {
	h := &handler{producer: p, log: log, now: time.Now, timeout: 15 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/events", h.events)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", h.ready)
	return mux
}

type batch struct {
	Events []event.Event `json:"events"`
}

func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	var b batch
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&b); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "body exceeds 1 MB"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed JSON: " + err.Error()})
		return
	}
	if err := event.ValidateBatch(b.Events, h.now()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	if err := h.producer.Produce(ctx, b.Events); err != nil {
		h.log.WarnContext(r.Context(), "kafka did not confirm the batch", "events", len(b.Events), "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "events not confirmed by Kafka, retry"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"accepted": len(b.Events)})
}

func (h *handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.producer.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
