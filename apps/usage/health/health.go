package health

import (
	"context"
	"net/http"
	"time"
)

type Service struct {
	// Ping reports whether Kafka is reachable.
	Ping func(context.Context) error
}

func NewService(ping func(context.Context) error) *Service {
	return &Service{Ping: ping}
}

func (s *Service) LiveHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Service) ReadyHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Ping(ctx); err != nil {
		http.Error(w, "kafka unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}
