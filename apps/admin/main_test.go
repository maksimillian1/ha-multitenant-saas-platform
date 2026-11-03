package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/auth"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/health"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/ui"
)

// TestRoutes checks that the patterns register without a conflict and that
// each path reaches its handler. Nobody is signed in.
func TestRoutes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authService := auth.NewService(nil, auth.NewCookies("0123456789abcdef0123456789abcdef"), "http://admin.test", "client", log)
	mux := routes(authService,
		newAPIService(core.Config{ZitadelURL: "http://zitadel.test", ZitadelToken: "token", ZitadelInstanceToken: "instance-token",
			AdminRole: "super-admin", PublicURL: "http://admin.test"}, authService, http.DefaultClient, log),
		ui.NewService(authService, log),
		health.NewService())

	for _, tc := range []struct {
		method, target string
		status         int
		contentType    string
	}{
		{http.MethodGet, "/healthz", http.StatusOK, ""},
		{http.MethodGet, "/api/me", http.StatusUnauthorized, "application/json"},
		{http.MethodGet, "/api/orgs", http.StatusUnauthorized, "application/json"},
		{http.MethodPost, "/api/users/42/lock", http.StatusUnauthorized, "application/json"},
		{http.MethodGet, "/api/projects/7/roles", http.StatusUnauthorized, "application/json"},
		{http.MethodGet, "/api/unknown", http.StatusNotFound, "application/json"},
		{http.MethodGet, "/", http.StatusFound, ""},
		{http.MethodGet, "/tenants/42", http.StatusFound, ""},
		{http.MethodPost, "/api/me", http.StatusMethodNotAllowed, ""},
		{http.MethodPut, "/api/orgs/1", http.StatusMethodNotAllowed, ""},
		{http.MethodGet, "/api/orgs/1/branding", http.StatusUnauthorized, "application/json"},
		{http.MethodGet, "/api/platform/branding/assets/logo", http.StatusUnauthorized, "application/json"},
		{http.MethodDelete, "/api/platform/branding", http.StatusMethodNotAllowed, ""},
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

type adminSession struct{}

func (adminSession) Session(*http.Request) (core.Session, bool) {
	return core.Session{Subject: "admin", Roles: []string{"super-admin"}, Expires: time.Now().Add(time.Hour)}, true
}

// TestBrandingTokensGoToTheirRoutes checks that the instance token reaches
// Zitadel only on the platform branding routes, and the admin app's token on
// every other call (HAMSP-37, AC-7).
func TestBrandingTokensGoToTheirRoutes(t *testing.T) {
	var (
		mu    sync.Mutex
		calls = map[string]string{} // path -> token
	)
	zitadel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path] = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/ListOrganizations"):
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []any{map[string]any{"id": "org-a", "name": "tenant-a"}},
				"details": map[string]any{"totalResult": "1"}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"policy": map[string]any{"primaryColor": "#5469d4"}})
		}
	}))
	defer zitadel.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	newAPIService(core.Config{ZitadelURL: zitadel.URL, ZitadelToken: "org-token", ZitadelInstanceToken: "instance-token",
		AdminRole: "super-admin", PublicURL: "http://admin.test"}, adminSession{}, zitadel.Client(), log).Register(mux)

	for _, target := range []string{"/api/orgs/org-a/branding", "/api/platform/branding"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d: %s", target, rec.Code, rec.Body)
		}
	}
	if len(calls) == 0 {
		t.Fatal("no call reached Zitadel")
	}
	for path, token := range calls {
		want := "org-token"
		if strings.HasPrefix(path, "/admin/v1/") {
			want = "instance-token"
		}
		if token != want {
			t.Errorf("%s was called with %q, want %q", path, token, want)
		}
	}
	for _, path := range []string{"/admin/v1/policies/label", "/management/v1/policies/label"} {
		if _, ok := calls[path]; !ok {
			t.Errorf("%s was not called; calls: %v", path, calls)
		}
	}
}
