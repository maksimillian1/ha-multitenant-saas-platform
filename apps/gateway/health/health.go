package health

import "net/http"

type Service struct {
	ready func() bool
}

func NewService(ready func() bool) *Service {
	return &Service{ready: ready}
}

func (s *Service) Handler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Service) ReadyHandler(w http.ResponseWriter, r *http.Request) {
	if s.ready != nil && !s.ready() {
		http.Error(w, "no signing keys", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}
