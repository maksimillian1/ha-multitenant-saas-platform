package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/auth"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/internal/fakes"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/lifecycle"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/store/storetest"
)

// Tokens of the fake checker. The token checks themselves are tested in
// package auth against real JWTs.
const (
	tokenManager = "manager-token"
	tokenNoRole  = "no-role-token"
	adminUser    = "312909075212468632"
)

type fakeChecker struct{}

func (fakeChecker) Check(_ context.Context, token string) (auth.Caller, error) {
	switch token {
	case tokenManager:
		return auth.Caller{Subject: "admin-app"}, nil
	case tokenNoRole:
		return auth.Caller{Subject: "someone"}, auth.ErrNoRole
	default:
		return auth.Caller{}, errors.New("invalid token")
	}
}

// syncBuffer is a log sink the handlers and the test can share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type env struct {
	srv  *httptest.Server
	deps *fakes.Deps
	svc  *lifecycle.Service
	logs *syncBuffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := storetest.New(t)
	deps := fakes.New()
	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	svc := lifecycle.NewService(db.Open(t), deps, deps, log, core.NewID)
	svc.StepTimeout = 200 * time.Millisecond
	srv := httptest.NewServer(NewServer(svc, log).Routes(fakeChecker{}))
	t.Cleanup(srv.Close)
	return &env{srv: srv, deps: deps, svc: svc, logs: logs}
}

type reply struct {
	status int
	body   []byte
}

func (r reply) tenant(t *testing.T) core.Tenant {
	t.Helper()
	var tn core.Tenant
	if err := json.Unmarshal(r.body, &tn); err != nil {
		t.Fatalf("body %s: %v", r.body, err)
	}
	return tn
}

func (r reply) errorBody(t *testing.T) core.ErrorBody {
	t.Helper()
	var e core.ErrorBody
	if err := json.Unmarshal(r.body, &e); err != nil || e.Error == "" {
		t.Fatalf("body %s is not an error: %v", r.body, err)
	}
	return e
}

func (e *env) do(t *testing.T, method, path, token, body string) reply {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if method != http.MethodGet {
		req.Header.Set(AdminHeader, adminUser)
	}
	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return reply{status: res.StatusCode, body: data}
}

func (e *env) create(t *testing.T, name string) core.Tenant {
	t.Helper()
	r := e.do(t, http.MethodPost, "/tenants", tokenManager, `{"name":"`+name+`","admin_email":"it@`+name+`.example"}`)
	if r.status != http.StatusCreated {
		t.Fatalf("create %s: %d %s", name, r.status, r.body)
	}
	return r.tenant(t)
}

// Verifies: tenant-management AC-1
func TestCreateTenantRunsEveryStep(t *testing.T) {
	e := newEnv(t)
	a := e.create(t, "acme")
	if a.State != core.StateActive || len(a.Parts) != 3 || a.CreatedBy != adminUser || a.AdminEmail != "it@acme.example" {
		t.Fatalf("created = %+v", a)
	}
	if org, admin, ns := e.deps.Exists(a.ID); !org || !admin || !ns {
		t.Fatalf("organization %v, admin %v, namespace %v", org, admin, ns)
	}

	got := e.do(t, http.MethodGet, "/tenants/"+a.ID, tokenManager, "")
	if got.status != http.StatusOK || got.tenant(t).State != core.StateActive {
		t.Fatalf("get: %d %s", got.status, got.body)
	}
	list := e.do(t, http.MethodGet, "/tenants", tokenManager, "")
	var l TenantList
	if err := json.Unmarshal(list.body, &l); err != nil || list.status != http.StatusOK || len(l.Tenants) != 1 {
		t.Fatalf("list: %d %s", list.status, list.body)
	}
}

type route struct{ method, path, body string }

func routes(id string) []route {
	return []route{
		{http.MethodGet, "/tenants", ""},
		{http.MethodPost, "/tenants", `{"name":"evil","admin_email":"x@evil.example"}`},
		{http.MethodGet, "/tenants/" + id, ""},
		{http.MethodPost, "/tenants/" + id + "/retry", ""},
		{http.MethodPost, "/tenants/" + id + "/suspend", ""},
		{http.MethodPost, "/tenants/" + id + "/resume", ""},
		{http.MethodDelete, "/tenants/" + id, ""},
	}
}

// Verifies: tenant-management AC-9; T-04, T-12
func TestEveryRouteRequiresTenantManager(t *testing.T) {
	e := newEnv(t)
	a := e.create(t, "acme")
	e.deps.ResetCalls()
	before := e.do(t, http.MethodGet, "/tenants/"+a.ID, tokenManager, "").tenant(t)

	for _, rt := range routes(a.ID) {
		for _, c := range []struct {
			token string
			want  int
		}{{"", http.StatusUnauthorized}, {"forged", http.StatusUnauthorized}, {tokenNoRole, http.StatusForbidden}} {
			r := e.do(t, rt.method, rt.path, c.token, rt.body)
			if r.status != c.want {
				t.Errorf("%s %s with %q: %d, want %d", rt.method, rt.path, c.token, r.status, c.want)
			}
			r.errorBody(t)
		}
	}
	if calls := e.deps.CallLog(); len(calls) != 0 {
		t.Fatalf("refused requests reached Zitadel or Kubernetes: %v", calls)
	}
	after := e.do(t, http.MethodGet, "/tenants/"+a.ID, tokenManager, "").tenant(t)
	if after.UpdatedAt != before.UpdatedAt || after.State != before.State {
		t.Fatalf("refused requests changed the tenant: %+v → %+v", before, after)
	}
	l := e.do(t, http.MethodGet, "/tenants?include_deleted=true", tokenManager, "")
	if strings.Contains(string(l.body), "evil") {
		t.Fatal("a refused create wrote a row")
	}

	// And with the role, every route is reachable.
	for _, rt := range routes(a.ID) {
		r := e.do(t, rt.method, rt.path, tokenManager, rt.body)
		if r.status == http.StatusUnauthorized || r.status == http.StatusForbidden {
			t.Errorf("%s %s with the role: %d %s", rt.method, rt.path, r.status, r.body)
		}
	}
}

// Verifies: tenant-management AC-11
func TestMutationsAreLogged(t *testing.T) {
	e := newEnv(t)
	a := e.create(t, "acme")
	for _, path := range []string{"/suspend", "/resume", "/suspend"} {
		if r := e.do(t, http.MethodPost, "/tenants/"+a.ID+path, tokenManager, ""); r.status != http.StatusOK {
			t.Fatalf("%s: %d %s", path, r.status, r.body)
		}
	}
	if r := e.do(t, http.MethodDelete, "/tenants/"+a.ID, tokenManager, ""); r.status != http.StatusOK {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}

	var actions []string
	for _, line := range strings.Split(strings.TrimSpace(e.logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if entry["msg"] != "tenant changed" {
			continue
		}
		if entry["admin"] != adminUser || entry["tenant_id"] != a.ID {
			t.Errorf("log entry %v", entry)
		}
		actions = append(actions, entry["action"].(string))
	}
	if strings.Join(actions, ",") != "create,suspend,resume,suspend,delete" {
		t.Fatalf("logged actions = %v", actions)
	}

	// A change without the acting admin is refused before anything happens.
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/tenants", strings.NewReader(`{"name":"globex","admin_email":"it@globex.example"}`))
	req.Header.Set("Authorization", "Bearer "+tokenManager)
	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("create without %s: %d", AdminHeader, res.StatusCode)
	}
}

// Verifies: tenant-management AC-4, AC-6, AC-7, AC-8; T-06
func TestErrorAnswers(t *testing.T) {
	e := newEnv(t)
	a := e.create(t, "acme")

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"name taken", http.MethodPost, "/tenants", `{"name":"ACME","admin_email":"x@acme.example"}`, http.StatusConflict},
		{"bad body", http.MethodPost, "/tenants", `{"name":""}`, http.StatusBadRequest},
		{"unknown tenant", http.MethodGet, "/tenants/33333333-3333-4333-8333-333333333333", "", http.StatusNotFound},
		{"not a tenant ID", http.MethodGet, "/tenants/..%2f..%2fetc", "", http.StatusNotFound},
		{"resume an active tenant", http.MethodPost, "/tenants/" + a.ID + "/resume", "", http.StatusConflict},
		{"delete an active tenant", http.MethodDelete, "/tenants/" + a.ID, "", http.StatusConflict},
		{"retry an active tenant", http.MethodPost, "/tenants/" + a.ID + "/retry", "", http.StatusConflict},
		{"no such route", http.MethodPut, "/tenants/" + a.ID, "", http.StatusMethodNotAllowed},
	}
	for _, c := range cases {
		r := e.do(t, c.method, c.path, tokenManager, c.body)
		if r.status != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, r.status, r.body, c.want)
		}
	}

	// A failing dependency: 502 with the tenant; a hanging one: 504.
	e.deps.FailNext["DeactivateOrganization"] = 1
	r := e.do(t, http.MethodPost, "/tenants/"+a.ID+"/suspend", tokenManager, "")
	if body := r.errorBody(t); r.status != http.StatusBadGateway || body.Tenant == nil ||
		body.Tenant.State != core.StateActive || body.Tenant.LastError == "" {
		t.Fatalf("failed suspend: %d %s", r.status, r.body)
	}
	e.deps.Mu.Lock()
	e.deps.Block["DeactivateOrganization"] = make(chan struct{})
	e.deps.Mu.Unlock()
	r = e.do(t, http.MethodPost, "/tenants/"+a.ID+"/suspend", tokenManager, "")
	if body := r.errorBody(t); r.status != http.StatusGatewayTimeout || body.Tenant == nil {
		t.Fatalf("timed out suspend: %d %s", r.status, r.body)
	}

	// Zitadel holds a foreign organization with the name: 409 with the tenant.
	e.deps.FailNext["CreateOrganization"] = 1
	e.deps.FailWith = core.ErrForeign
	r = e.do(t, http.MethodPost, "/tenants", tokenManager, `{"name":"globex","admin_email":"it@globex.example"}`)
	if body := r.errorBody(t); r.status != http.StatusConflict || body.Tenant == nil || body.Tenant.State != core.StateProvisioning {
		t.Fatalf("foreign organization: %d %s", r.status, r.body)
	}
}
