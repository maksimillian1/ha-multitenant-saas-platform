package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleweb/core"
)

// fakeIssuer serves only OIDC discovery, enough to build the relying party.
func fakeIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/oauth/v2/authorize",
			"token_endpoint":         srv.URL + "/oauth/v2/token",
			"jwks_uri":               srv.URL + "/oauth/v2/keys",
			"end_session_endpoint":   srv.URL + "/oidc/v1/end_session",
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	issuer := fakeIssuer(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := core.Config{Issuer: issuer.URL, ClientID: "web", ClientSecret: "s", PublicURL: "http://web.test", Audience: "platform"} // gitleaks:allow
	cookies := NewCookies(false)
	provider, err := NewProvider(context.Background(), log, cfg, issuer.Client(), cookies)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(provider, NewStore(), cfg.PublicURL, cfg.ClientID, log)
}

func testTokens(org string) *oidc.Tokens[*oidc.IDTokenClaims] {
	claims := &oidc.IDTokenClaims{}
	claims.Subject = "user-1"
	claims.Name = "Alice"
	claims.Claims = map[string]any{core.OrgClaim: org}
	return &oidc.Tokens[*oidc.IDTokenClaims]{
		IDTokenClaims: claims, IDToken: "id-token-secret",
		Token: oidcToken("access-token-secret", "refresh-token-secret"),
	}
}

// Verifies: tenant-self-admin AC-7
func TestLoginSetsOnlySessionCookie(t *testing.T) {
	s := newTestService(t)

	// The login redirects to the issuer with PKCE.
	rec := httptest.NewRecorder()
	s.LoginHandler(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login: status %d, want 302", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if q := loc.Query(); q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" {
		t.Errorf("login redirect %s: want the code flow with an S256 code challenge", loc)
	}
	if !strings.Contains(loc.Query().Get("scope"), "urn:zitadel:iam:org:project:id:platform:aud") {
		t.Errorf("login scope %q lacks the platform API audience", loc.Query().Get("scope"))
	}

	// After the code exchange the browser gets one opaque, HttpOnly, SameSite=Lax cookie.
	rec = httptest.NewRecorder()
	s.signIn(rec, httptest.NewRequest(http.MethodGet, "/auth/callback", nil), testTokens("org-a"), "", nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback: status %d to %q, want 302 to /", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != SessionCookie {
		t.Fatalf("callback set cookies %v, want only %s", cookies, SessionCookie)
	}
	c := cookies[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie HttpOnly=%v SameSite=%v, want HttpOnly and Lax", c.HttpOnly, c.SameSite)
	}
	for _, secret := range []string{"access-token-secret", "refresh-token-secret", "id-token-secret"} {
		if strings.Contains(c.Value, secret) || strings.Contains(rec.Body.String(), secret) {
			t.Errorf("the browser got the token %s", secret)
		}
	}
	session, ok := s.Store.Get(c.Value)
	if !ok || session.AccessToken != "access-token-secret" || session.OrgID != "org-a" {
		t.Errorf("stored session = %+v, %v; want the tokens and org-a", session, ok)
	}
}

// Verifies: tenant-self-admin AC-7
func TestCallbackWithoutOrganizationFails(t *testing.T) {
	s := newTestService(t)
	rec := httptest.NewRecorder()
	s.signIn(rec, httptest.NewRequest(http.MethodGet, "/auth/callback", nil), testTokens(""), "", nil)
	if rec.Code != http.StatusInternalServerError || len(rec.Result().Cookies()) != 0 {
		t.Errorf("callback without organization: status %d, cookies %v; want 500 and no session", rec.Code, rec.Result().Cookies())
	}
}

// Verifies: tenant-self-admin AC-10
func TestLogoutEndsSession(t *testing.T) {
	s := newTestService(t)
	id := s.Store.Create(core.Session{Subject: "user-1", IDToken: "id-token", AccessToken: "a", Expires: time.Now().Add(time.Hour)})
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: id})

	rec := httptest.NewRecorder()
	s.LogoutHandler(rec, req)

	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || !strings.HasSuffix(loc.Path, "/oidc/v1/end_session") {
		t.Fatalf("logout: status %d to %s, want 302 to Zitadel's end_session", rec.Code, loc)
	}
	if q := loc.Query(); q.Get("id_token_hint") != "id-token" || q.Get("post_logout_redirect_uri") != "http://web.test/" {
		t.Errorf("end_session query %v: want id_token_hint and the post-logout redirect", q)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != SessionCookie || cookies[0].MaxAge >= 0 {
		t.Errorf("logout cookies %v, want the session cookie removed", cookies)
	}
	if _, _, err := s.Session(req); err == nil {
		t.Error("the old cookie still opens a session after logout")
	}
}

func TestExpiredSessionIsGone(t *testing.T) {
	s := newTestService(t)
	id := s.Store.Create(core.Session{Subject: "user-1", Expires: time.Now().Add(-time.Second)})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: id})
	if _, _, err := s.Session(req); err == nil {
		t.Error("an expired session still opens the page")
	}
}

// Verifies: gateway-auth AC-10
func TestLoginWhileZitadelIsDownShowsUnavailablePage(t *testing.T) {
	s := newTestService(t)
	s.Probe = func(context.Context) error { return errors.New("connection refused") }
	rec := httptest.NewRecorder()
	s.LoginHandler(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `id="sign-in-unavailable"`) {
		t.Fatalf("login with Zitadel down: status %d, body %.200s; want 503 and the unavailable page", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") != "" {
		t.Error("login with Zitadel down still redirects")
	}
}

func TestProbeIssuer(t *testing.T) {
	issuer := fakeIssuer(t)
	if err := ProbeIssuer(context.Background(), issuer.Client(), issuer.URL); err != nil {
		t.Errorf("reachable issuer: %v", err)
	}
	issuer.Close()
	if err := ProbeIssuer(context.Background(), issuer.Client(), issuer.URL); err == nil {
		t.Error("a closed issuer is reported reachable")
	}
}
