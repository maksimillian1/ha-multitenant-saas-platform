package main

import (
	"context"
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
		newAPIService(core.Config{ZitadelURL: "http://zitadel.test",
			AdminRole: "super-admin", PublicURL: "http://admin.test"}, authService, testTokens("token"), http.DefaultClient, log),
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

// testTokens is a fixed access token in place of the key's tokens.
type testTokens string

func (s testTokens) Token(context.Context) (string, error) { return string(s), nil }
func (testTokens) Invalidate(string)                       {}

// TestBrandingCallsUseTheAdminAppAccount checks that the organization and
// the platform branding routes both call Zitadel as admin-app. Until HAMSP-36
// the platform routes used a second account with IAM_OWNER (HAMSP-37, AC-7).
//
// Verifies: admin-zitadel AC-14
func TestBrandingCallsUseTheAdminAppAccount(t *testing.T) {
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
	newAPIService(core.Config{ZitadelURL: zitadel.URL,
		AdminRole: "super-admin", PublicURL: "http://admin.test"}, adminSession{}, testTokens("admin-app-token"), zitadel.Client(), log).Register(mux)

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
		if want := "admin-app-token"; token != want {
			t.Errorf("%s was called with %q, want %q", path, token, want)
		}
	}
	for _, path := range []string{"/admin/v1/policies/label", "/management/v1/policies/label"} {
		if _, ok := calls[path]; !ok {
			t.Errorf("%s was not called; calls: %v", path, calls)
		}
	}
}

// TestTokensNeverReachTheBrowser calls every branding route against a
// Zitadel that echoes the caller's token wherever it can: in a header, in
// the message of failed writes and as the content of an asset. Neither token
// may appear in what the BFF answers (HAMSP-37, AC-8).
func TestTokensNeverReachTheBrowser(t *testing.T) {
	zitadel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("X-Echo-Token", token)
		switch {
		case strings.HasSuffix(r.URL.Path, "/ListOrganizations"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []any{map[string]any{"id": "org-a", "name": "tenant-a"}}})
		case strings.HasPrefix(r.URL.Path, "/assets/") && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, "token "+token)
		case r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"policy": map[string]any{"primaryColor": "#5469d4", "token": token}})
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "internal", "message": "failed for token " + token})
		}
	}))
	defer zitadel.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	newAPIService(core.Config{ZitadelURL: zitadel.URL,
		AdminRole: "super-admin", PublicURL: "http://admin.test"}, adminSession{}, testTokens("admin-app-token-secret"), zitadel.Client(), log).Register(mux)

	settings := `{"primary_color":"#7c3aed","background_color":"#fafafa","warn_color":"#cd3d56","font_color":"#000000",
		"primary_color_dark":"#c4b5fd","background_color_dark":"#111827","warn_color_dark":"#ff3b5b","font_color_dark":"#ffffff",
		"hide_login_name_suffix":false,"disable_watermark":false,"theme_mode":"auto"}`
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)
	upload := "--b\r\nContent-Disposition: form-data; name=\"file\"; filename=\"x.png\"\r\nContent-Type: image/png\r\n\r\n" + png + "\r\n--b--\r\n"

	routes := []struct{ method, path, contentType, body string }{
		{"GET", "/api/me", "", ""},
	}
	for _, base := range []string{"/api/orgs/org-a/branding", "/api/platform/branding"} {
		routes = append(routes,
			struct{ method, path, contentType, body string }{"GET", base, "", ""},
			struct{ method, path, contentType, body string }{"PUT", base, "application/json", settings},
			struct{ method, path, contentType, body string }{"POST", base + "/publish", "", ""},
			struct{ method, path, contentType, body string }{"GET", base + "/assets/logo", "", ""},
			struct{ method, path, contentType, body string }{"GET", base + "/assets/logo?version=draft", "", ""},
			struct{ method, path, contentType, body string }{"PUT", base + "/assets/logo", "multipart/form-data; boundary=b", upload},
			struct{ method, path, contentType, body string }{"DELETE", base + "/assets/logo", "", ""},
		)
	}
	routes = append(routes, struct{ method, path, contentType, body string }{"DELETE", "/api/orgs/org-a/branding", "", ""})

	for _, route := range routes {
		r := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
		r.Host = "admin.test"
		r.Header.Set("Origin", "http://admin.test")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		if route.contentType != "" {
			r.Header.Set("Content-Type", route.contentType)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("%s %s: status %d; the request did not reach the handler", route.method, route.path, rec.Code)
		}
		answer := rec.Body.String()
		for name, values := range rec.Header() {
			answer += "\n" + name + ": " + strings.Join(values, ",")
		}
		for _, token := range []string{"admin-app-token-secret"} {
			if strings.Contains(answer, token) {
				t.Errorf("%s %s answered with the token %s:\n%s", route.method, route.path, token, answer)
			}
		}
	}
}
