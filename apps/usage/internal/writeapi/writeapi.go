// Package writeapi is UsageWriteAPI (docs/contracts/usage-event): it
// validates a batch of usage events and answers 202 only after Kafka confirmed
// every event (ADR-0005).
package writeapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/httpx"
)

const maxBody = 1 << 20

type Service struct {
	Producer core.Producer
	Log      *slog.Logger
	now      func() time.Time
	timeout  time.Duration
}

func NewService(producer core.Producer, log *slog.Logger) *Service {
	return &Service{Producer: producer, Log: log, now: time.Now, timeout: 15 * time.Second}
}

type batch struct {
	Events []core.Event `json:"events"`
}

// Register adds the endpoint of every HTTP contract version this service reads
// (docs/contracts/usage-event/participants.json). A new version is served next
// to the old ones until no sender uses the old one.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/events", s.EventsHandler)
}

func (s *Service) EventsHandler(w http.ResponseWriter, r *http.Request) {
	var b batch
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&b); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpx.WriteError(w, http.StatusRequestEntityTooLarge, "body exceeds 1 MB")
			return
		}
		httpx.WriteError(w, http.StatusBadRequest, "malformed JSON: "+err.Error())
		return
	}
	if err := core.ValidateBatch(b.Events, s.now()); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	if err := s.Producer.Produce(ctx, b.Events); err != nil {
		s.Log.WarnContext(r.Context(), "kafka did not confirm the batch", "events", len(b.Events), "err", err)
		httpx.WriteError(w, http.StatusServiceUnavailable, "events not confirmed by Kafka, retry")
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]int{"accepted": len(b.Events)})
}
