package auth

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
)

const (
	testKey    = "0123456789abcdef0123456789abcdef"
	endSession = "http://zitadel.test/oidc/v1/end_session"
)

// fakeProvider is the part of the relying party that LogoutHandler uses.
type fakeProvider struct{ rp.RelyingParty }

func (fakeProvider) GetEndSessionEndpoint() string { return endSession }

func newTestService(key string) *Service {
	return NewService(fakeProvider{}, NewCookies(key), "http://admin.test", "client", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// withSession returns a request carrying the cookie that s set for session.
func withSession(t *testing.T, s *Service, session core.Session) *http.Request {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := s.setSession(rec, session); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		r.AddCookie(c)
	}
	return r
}

func TestSessionRoundTrip(t *testing.T) {
	s := newTestService(testKey)
	want := core.Session{Subject: "7", Name: "Admin", Roles: []string{"super-admin"}, Expires: time.Now().Add(time.Hour).Truncate(time.Second)}
	got, ok := s.Session(withSession(t, s, want))
	if !ok || got.Subject != want.Subject || !got.HasRole("super-admin") || !got.Expires.Equal(want.Expires) {
		t.Fatalf("session = %+v, %v; want %+v", got, ok, want)
	}
}

func TestSessionCookieCannotBeForged(t *testing.T) {
	// A cookie made with another key, as an attacker without SESSION_KEY would.
	r := withSession(t, newTestService(strings.Repeat("x", 32)), core.Session{
		Subject: "1", Roles: []string{"super-admin"}, Expires: time.Now().Add(time.Hour),
	})
	if _, ok := newTestService(testKey).Session(r); ok {
		t.Fatal("a cookie signed with another key was accepted")
	}
	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	plain.AddCookie(&http.Cookie{Name: sessionCookie, Value: `{"sub":"1","roles":["super-admin"],"exp":"2999-01-01T00:00:00Z"}`})
	if _, ok := newTestService(testKey).Session(plain); ok {
		t.Fatal("a plain JSON cookie was accepted")
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	s := newTestService(testKey)
	r := withSession(t, s, core.Session{Subject: "7", Roles: []string{"super-admin"}, Expires: time.Now().Add(-time.Minute)})
	if _, ok := s.Session(r); ok {
		t.Fatal("an expired session was accepted")
	}
}

func TestSessionFromClaimsReadsZitadelRoles(t *testing.T) {
	claims := &oidc.IDTokenClaims{
		TokenClaims:     oidc.TokenClaims{Subject: "7"},
		UserInfoProfile: oidc.UserInfoProfile{PreferredUsername: "super-admin"},
		UserInfoEmail:   oidc.UserInfoEmail{Email: "a@example.test"},
		Claims: map[string]any{
			core.RolesClaim: map[string]any{"super-admin": map[string]any{"1": "org"}, "auditor": map[string]any{"1": "org"}},
		},
	}
	now := time.Now()
	s := SessionFromClaims(claims, now)
	if s.Subject != "7" || s.Name != "super-admin" || s.Email != "a@example.test" ||
		!slices.Equal(s.Roles, []string{"auditor", "super-admin"}) || !s.Expires.Equal(now.Add(sessionLifetime)) {
		t.Fatalf("session = %+v", s)
	}
	if roles := SessionFromClaims(&oidc.IDTokenClaims{TokenClaims: oidc.TokenClaims{Subject: "8"}}, now).Roles; len(roles) != 0 {
		t.Fatalf("roles without a roles claim = %v", roles)
	}
}

func TestLogoutClearsSession(t *testing.T) {
	s := newTestService(testKey)
	rec := httptest.NewRecorder()
	s.LogoutHandler(rec, httptest.NewRequest(http.MethodPost, "/auth/logout", nil))
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), endSession+"?") {
		t.Fatalf("status %d, Location %q; want 302 to the end_session endpoint", rec.Code, rec.Header().Get("Location"))
	}
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		cleared = cleared || (c.Name == sessionCookie && c.MaxAge < 0)
	}
	if !cleared {
		t.Fatal("logout did not delete the session cookie")
	}
}
