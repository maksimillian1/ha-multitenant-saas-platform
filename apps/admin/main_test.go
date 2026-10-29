package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/api"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/auth"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/health"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/ui"
)

// TestRoutes checks that the patterns register without a conflict and that
// each path reaches its handler. Nobody is signed in.
func TestRoutes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authService := auth.NewService(nil, auth.NewCookies("0123456789abcdef0123456789abcdef"), "http://admin.test", "client", log)
	mux := routes(authService,
		api.NewService(authService, "super-admin", log),
		ui.NewService(authService, log),
		health.NewService())

	for _, tc := range []struct {
		method, target string
		status         int
		contentType    string
	}{
		{http.MethodGet, "/healthz", http.StatusOK, ""},
		{http.MethodGet, "/api/me", http.StatusUnauthorized, "application/json"},
		{http.MethodGet, "/api/unknown", http.StatusNotFound, "application/json"},
		{http.MethodGet, "/", http.StatusFound, ""},
		{http.MethodGet, "/tenants/42", http.StatusFound, ""},
		{http.MethodPost, "/api/me", http.StatusMethodNotAllowed, ""},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.target, nil))
		if rec.Code != tc.status {
			t.Errorf("%s %s: status %d, want %d", tc.method, tc.target, rec.Code, tc.status)
		}
		if tc.contentType != "" && rec.Header().Get("Content-Type") != tc.contentType {
			t.Errorf("%s %s: Content-Type %q, want %q", tc.method, tc.target, rec.Header().Get("Content-Type"), tc.contentType)
		}
	}
}
