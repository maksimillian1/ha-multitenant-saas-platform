// Package api registers HTTP routes.
package api

import (
	"net/http"

	"example.com/fixture/internal/app"
)

// Routes registers the handlers. The debug route is built at runtime.
func Routes(mux *http.ServeMux, s *app.Service, prefix string) {
	mux.HandleFunc("POST /v1/events", func(w http.ResponseWriter, r *http.Request) {})
	mux.Handle("GET /healthz", http.NotFoundHandler())
	mux.HandleFunc(prefix+"/debug", func(w http.ResponseWriter, r *http.Request) {})
}
