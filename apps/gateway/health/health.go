package health

import "net/http"

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Handler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
