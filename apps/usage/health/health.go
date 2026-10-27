package health

import (
	"context"
	"net/http"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/core"
)

type Service struct {
	// Ping reports whether the dependency the service needs is reachable. A
	// nil Ping makes ReadyHandler answer 200.
	Ping func(context.Context) error
}

func NewService(ping func(context.Context) error) *Service {
	return &Service{Ping: ping}
}

func (s *Service) LiveHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Service) ReadyHandler(w http.ResponseWriter, r *http.Request) {
	if s.Ping == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Ping(ctx); err != nil {
		core.WriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}
