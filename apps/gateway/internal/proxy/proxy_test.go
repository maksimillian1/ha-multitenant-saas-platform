package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeAuth accepts "token-<tenant>" and returns <tenant>.
type fakeAuth struct{}

func (fakeAuth) Tenant(_ context.Context, token string) (string, error) {
	tenant, ok := strings.CutPrefix(token, "token-")
	if !ok {
		return "", errors.New("bad token")
	}
	return tenant, nil
}

type seen struct {
	calls  int
	path   string
	header http.Header
}

func upstream(t *testing.T, name string, s *seen) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls++
		s.path = r.URL.Path
		s.header = r.Header.Clone()
		_, _ = io.WriteString(w, name)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u
}

func newGateway(t *testing.T) (*Gateway, *seen, *seen) {
	t.Helper()
	items, archive := &seen{}, &seen{}
	g := New(fakeAuth{}, []Route{
		{Prefix: "/items", Upstream: upstream(t, "items", items)},
		{Prefix: "/items/archive", Upstream: upstream(t, "archive", archive)},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return g, items, archive
}

func send(g http.Handler, path, auth string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func TestRequestsWithoutValidTokenNeverReachUpstream(t *testing.T) {
	g, items, _ := newGateway(t)
	for _, auth := range []string{"", "Bearer wrong", "Basic token-a"} {
		if w := send(g, "/items", auth); w.Code != http.StatusUnauthorized {
			t.Errorf("auth %q: status %d, want 401", auth, w.Code)
		}
	}
	// Unknown paths also answer 401 without a token, so routes are not probed.
	if w := send(g, "/nope", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("unknown path without token: status %d, want 401", w.Code)
	}
	if items.calls != 0 {
		t.Fatalf("upstream called %d times", items.calls)
	}
}

func TestTenantHeaderComesFromTokenOnly(t *testing.T) {
	g, items, _ := newGateway(t)
	w := send(g, "/items/42", "Bearer token-a", TenantHeader, "b")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if got := items.header.Values(TenantHeader); len(got) != 1 || got[0] != "a" {
		t.Fatalf("upstream %s = %v, want [a]", TenantHeader, got)
	}
	if items.header.Get("Authorization") != "" {
		t.Fatal("the token was forwarded upstream")
	}
	if items.path != "/items/42" {
		t.Fatalf("upstream path = %q, want /items/42", items.path)
	}
}

func TestRoutesMatchByLongestPrefixOnPathBoundaries(t *testing.T) {
	g, _, _ := newGateway(t)
	for path, want := range map[string]string{
		"/items":           "items",
		"/items/1":         "items",
		"/items/archive":   "archive",
		"/items/archive/2": "archive",
		"/itemsx":          "",
		"/other":           "",
	} {
		w := send(g, path, "Bearer token-a")
		if want == "" {
			if w.Code != http.StatusNotFound {
				t.Errorf("%s: status %d, want 404", path, w.Code)
			}
			continue
		}
		if w.Body.String() != want {
			t.Errorf("%s: routed to %q, want %q", path, w.Body, want)
		}
	}
}

func TestUnreachableUpstreamIs502(t *testing.T) {
	down, _ := url.Parse("http://127.0.0.1:1")
	g := New(fakeAuth{}, []Route{{Prefix: "/items", Upstream: down}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if w := send(g, "/items", "Bearer token-a"); w.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", w.Code)
	}
}

func TestParseRoutes(t *testing.T) {
	routes, err := ParseRoutes(" /items=http://exampleapp:8080 , /a/=http://a:1")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0].Prefix != "/items" || routes[1].Prefix != "/a" || routes[0].Upstream.Host != "exampleapp:8080" {
		t.Fatalf("routes = %+v", routes)
	}
	for _, bad := range []string{"", "items=http://x", "/items=nohost", "/items"} {
		if _, err := ParseRoutes(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}
