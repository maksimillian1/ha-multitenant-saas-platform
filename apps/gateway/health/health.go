package health

import "net/http"

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Handler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// Probe is a temporary type to test the c4sync PR comment (HAMSP-73 AC-13).
type Probe struct{}
