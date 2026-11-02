package proxy

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
	"sync"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/gateway/core"
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

func newGateway(t *testing.T) (http.Handler, *seen, *seen) {
	t.Helper()
	items, archive := &seen{}, &seen{}
	s := NewService(fakeAuth{}, []Route{
		{Prefix: "/items", Upstream: upstream(t, "items", items)},
		{Prefix: "/items/archive", Upstream: upstream(t, "archive", archive)},
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return http.HandlerFunc(s.Handler), items, archive
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
	w := send(g, "/items/42", "Bearer token-a", core.TenantHeader, "b")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if got := items.header.Values(core.TenantHeader); len(got) != 1 || got[0] != "a" {
		t.Fatalf("upstream %s = %v, want [a]", core.TenantHeader, got)
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
	s := NewService(fakeAuth{}, []Route{{Prefix: "/items", Upstream: down}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if w := send(http.HandlerFunc(s.Handler), "/items", "Bearer token-a"); w.Code != http.StatusBadGateway {
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

type fakeReporter struct {
	mu     sync.Mutex
	events []core.UsageEvent
}

func (f *fakeReporter) Report(e core.UsageEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func reportingGateway(t *testing.T) (http.Handler, *fakeReporter) {
	t.Helper()
	rep := &fakeReporter{}
	down, _ := url.Parse("http://127.0.0.1:1")
	s := NewService(fakeAuth{}, []Route{
		{Prefix: "/items", Upstream: upstream(t, "items", &seen{})},
		{Prefix: "/down", Upstream: down},
	}, rep, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return http.HandlerFunc(s.Handler), rep
}

// AC-1: a served request becomes one api_calls event of the caller's tenant
// with method, path without query string, status, duration and bytes.
func TestUsageEventDescribesTheRequest(t *testing.T) {
	g, rep := reportingGateway(t)
	r := httptest.NewRequest(http.MethodPost, "/items/42?secret=s3cr3t", strings.NewReader("hello"))
	r.Header.Set("Authorization", "Bearer token-a")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if len(rep.events) != 1 {
		t.Fatalf("%d usage events, want 1", len(rep.events))
	}
	e := rep.events[0]
	if e.TenantID != "a" || e.Metric != "api_calls" || e.Quantity != 1 || e.ID == "" || e.Time.IsZero() {
		t.Fatalf("event = %+v", e)
	}
	a := e.Attributes
	if a["method"] != "POST" || a["path"] != "/items/42" || a["status"] != 200 ||
		a["request_bytes"] != int64(5) || a["response_bytes"] != int64(len("items")) {
		t.Fatalf("attributes = %v", a)
	}
	if _, ok := a["duration_ms"]; !ok {
		t.Fatal("no duration_ms")
	}
	if raw, _ := json.Marshal(e); strings.Contains(string(raw), "s3cr3t") || strings.Contains(string(raw), "token-a") {
		t.Fatalf("the event leaks the query string or the token: %s", raw)
	}
}

// Requests served for a tenant are reported whatever their status.
func TestUsageEventKeepsErrorStatus(t *testing.T) {
	g, rep := reportingGateway(t)
	send(g, "/nope", "Bearer token-a")
	send(g, "/down/1", "Bearer token-b")
	if len(rep.events) != 2 {
		t.Fatalf("%d usage events, want 2", len(rep.events))
	}
	if rep.events[0].Attributes["status"] != http.StatusNotFound || rep.events[1].Attributes["status"] != http.StatusBadGateway ||
		rep.events[1].TenantID != "b" {
		t.Fatalf("events = %+v", rep.events)
	}
}

// AC-2: requests rejected with 401 have no tenant and produce no usage event.
func TestRejectedRequestsAreNotReported(t *testing.T) {
	g, rep := reportingGateway(t)
	for _, auth := range []string{"", "Bearer wrong", "Basic token-a"} {
		if w := send(g, "/items", auth); w.Code != http.StatusUnauthorized {
			t.Fatalf("auth %q: status %d", auth, w.Code)
		}
	}
	if len(rep.events) != 0 {
		t.Fatalf("rejected requests produced usage events: %+v", rep.events)
	}
}
