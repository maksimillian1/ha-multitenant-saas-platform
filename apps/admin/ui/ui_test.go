package ui

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
)

type fakeSessions struct {
	session core.Session
	ok      bool
}

func (f fakeSessions) Session(*http.Request) (core.Session, bool) { return f.session, f.ok }

var (
	signedIn = fakeSessions{ok: true, session: core.Session{Subject: "7", Roles: []string{"viewer"}, Expires: time.Now().Add(time.Hour)}}
	spa      = fstest.MapFS{
		"index.html":          {Data: []byte(`<div id="root"></div><script src="/assets/index-abc.js"></script>`)},
		"assets/index-abc.js": {Data: []byte("console.log('admin')")},
		"favicon.svg":         {Data: []byte("<svg/>")},
	}
)

func get(sessions fakeSessions, files fstest.MapFS, target string) *httptest.ResponseRecorder {
	svc := &Service{Sessions: sessions, Files: files, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec := httptest.NewRecorder()
	svc.Handler(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestWithoutSessionRedirectsToLogin(t *testing.T) {
	for _, target := range []string{"/", "/assets/index-abc.js", "/tenants"} {
		rec := get(fakeSessions{}, spa, target)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/auth/login" {
			t.Fatalf("%s: status %d, Location %q; want 302 to /auth/login", target, rec.Code, rec.Header().Get("Location"))
		}
		if strings.Contains(rec.Body.String(), "root") || strings.Contains(rec.Body.String(), "admin") {
			t.Fatalf("%s: SPA content was sent without a session", target)
		}
	}
}

func TestSignedInUserGetsTheSPA(t *testing.T) {
	rec := get(signedIn, spa, "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="root"`) {
		t.Fatalf("status %d, body %q; want index.html", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("index.html may be cached")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Error("no CSP")
	}
}

func TestHashedAssetsHaveLongCache(t *testing.T) {
	rec := get(signedIn, spa, "/assets/index-abc.js")
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log('admin')" {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") || !strings.Contains(cc, "private") {
		t.Errorf("Cache-Control %q, want private immutable", cc)
	}
}

func TestUnknownPathIsARouteOfTheSPA(t *testing.T) {
	for _, target := range []string{"/tenants/42", "/index.html", "/../index.html"} {
		rec := get(signedIn, spa, target)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Fatalf("%s: status %d, want index.html", target, rec.Code)
		}
	}
}

func TestUnbuiltSPAIsReported(t *testing.T) {
	rec := get(signedIn, fstest.MapFS{}, "/")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
}
