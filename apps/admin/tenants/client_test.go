package tenants

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const examples = "../../../docs/contracts/tenants/examples"

func example(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(examples, name)) //nolint:gosec // G304: a fixed file of the contract
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fakeTM is Zitadel's token endpoint and Tenant Management in one server.
// It answers each Tenant Management path with the body set for it.
type fakeTM struct {
	mu       sync.Mutex
	tokens   int
	requests []*http.Request
	bodies   []string
	answers  map[string]answer // "METHOD /path"
}

type answer struct {
	status int
	body   []byte
}

func newFake(t *testing.T) (*fakeTM, *Client) {
	t.Helper()
	f := &fakeTM{answers: map[string]answer{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	c := New(t.Context(), Config{URL: srv.URL + "/tm/", TokenURL: srv.URL + "/oauth/v2/token", ClientID: "admin-tenants-client",
		ClientSecret: "s3cret", Audience: "p-tm", Timeout: 5 * time.Second}, srv.Client()) // gitleaks:allow
	return f, c
}

func (f *fakeTM) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/oauth/v2/token" {
		user, pass, ok := r.BasicAuth()
		if err := r.ParseForm(); err != nil || !ok || user != "admin-tenants-client" || pass != "s3cret" ||
			r.PostForm.Get("grant_type") != "client_credentials" ||
			r.PostForm.Get("scope") != "openid urn:zitadel:iam:org:project:id:p-tm:aud urn:zitadel:iam:org:projects:roles" {
			http.Error(w, `{"error": "invalid_client"}`, http.StatusUnauthorized)
			return
		}
		f.tokens++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token": "jwt-1", "token_type": "Bearer", "expires_in": 3600}`))
		return
	}
	var body strings.Builder
	if r.Body != nil {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		body.Write(buf[:n])
	}
	f.requests = append(f.requests, r.Clone(r.Context()))
	f.bodies = append(f.bodies, body.String())
	a, ok := f.answers[r.Method+" "+strings.TrimPrefix(r.URL.RequestURI(), "/tm")]
	if !ok {
		http.Error(w, `{"error": "not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(a.status)
	_, _ = w.Write(a.body)
}

// TestClientSendsTokenAndAdmin: every call carries the machine token, which
// is fetched once and reused, and every change names the admin. The
// answers are the contract's examples.
//
// Verifies: AC-14 (T-02: the client against docs/contracts/tenants/examples)
func TestClientSendsTokenAndAdmin(t *testing.T) {
	f, c := newFake(t)
	active := example(t, "tenant-active.json")
	id := "0b9a5c1e-7d2f-4e8a-9c3b-5f6a7d8e9f01"
	f.answers["POST /tenants"] = answer{http.StatusCreated, active}
	f.answers["GET /tenants/"+id] = answer{http.StatusOK, active}
	f.answers["GET /tenants?include_deleted=true"] = answer{http.StatusOK, []byte(`{"tenants": [` + string(active) + `]}`)}
	f.answers["DELETE /tenants/"+id] = answer{http.StatusOK, example(t, "tenant-deleted.json")}
	ctx := context.Background()

	created, err := c.Create(ctx, "312909075212468632", Create{Name: "Acme Corp", AdminEmail: "it@acme.example"})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != id || created.State != "active" || !slices.Equal(created.Parts, []string{"organization", "admin", "namespace"}) {
		t.Errorf("created %+v", created)
	}
	if _, err := c.Get(ctx, id); err != nil {
		t.Fatal(err)
	}
	list, err := c.List(ctx, true)
	if err != nil || len(list) != 1 {
		t.Fatalf("list %+v, %v", list, err)
	}
	tomb, err := c.Delete(ctx, "312909075212468632", id)
	if err != nil {
		t.Fatal(err)
	}
	if tomb.State != "deleted" || tomb.Name != "" || tomb.DeletedAt == nil {
		t.Errorf("tombstone %+v", tomb)
	}

	if f.tokens != 1 {
		t.Errorf("%d token requests, want 1 (cached)", f.tokens)
	}
	for i, r := range f.requests {
		if r.Header.Get("Authorization") != "Bearer jwt-1" {
			t.Errorf("%s %s: Authorization %q", r.Method, r.URL, r.Header.Get("Authorization"))
		}
		admin := r.Header.Get(AdminHeader)
		if r.Method == http.MethodGet && admin != "" || r.Method != http.MethodGet && admin != "312909075212468632" {
			t.Errorf("%s %s: %s %q", r.Method, r.URL, AdminHeader, admin)
		}
		if r.Method == http.MethodPost && f.bodies[i] != `{"name":"Acme Corp","admin_email":"it@acme.example"}` {
			t.Errorf("create body %s", f.bodies[i])
		}
	}
}

func TestClientErrors(t *testing.T) {
	f, c := newFake(t)
	id := "0b9a5c1e-7d2f-4e8a-9c3b-5f6a7d8e9f01"
	f.answers["POST /tenants/"+id+"/retry"] = answer{http.StatusBadGateway, example(t, "error-step-failed.json")}
	f.answers["POST /tenants/"+id+"/suspend"] = answer{http.StatusConflict, []byte(`{"error": "the tenant is provisioning"}`)}
	f.answers["POST /tenants/"+id+"/resume"] = answer{http.StatusServiceUnavailable, []byte(`<html>upstream</html>`)}
	ctx := context.Background()

	_, err := c.Retry(ctx, "admin", id)
	var terr *Error
	if !errors.As(err, &terr) || terr.Status != 502 || !strings.HasPrefix(terr.Message, "step namespace") ||
		terr.Tenant == nil || terr.Tenant.State != "provisioning" || terr.Tenant.LastError == "" {
		t.Fatalf("step failure: %#v", err)
	}
	if _, err := c.Suspend(ctx, "admin", id); !errors.As(err, &terr) || terr.Status != 409 || terr.Tenant != nil {
		t.Fatalf("conflict: %#v", err)
	}
	if _, err := c.Resume(ctx, "admin", id); !errors.As(err, &terr) || terr.Status != 503 || terr.Message != "Service Unavailable" {
		t.Fatalf("non-JSON answer: %#v", err)
	}
	if _, err := c.Get(ctx, "../oauth/v2/token"); !errors.As(err, &terr) || terr.Status != 404 {
		t.Fatalf("an ID with a slash must stay one path segment: %#v", err)
	}

	before := len(f.requests)
	if _, err := c.Suspend(ctx, "", id); err == nil {
		t.Fatal("a change without the admin's user ID was sent")
	}
	if len(f.requests) != before {
		t.Error("a change without the admin's user ID reached Tenant Management")
	}
}

func TestClientWithRefusedCredentials(t *testing.T) {
	f, _ := newFake(t)
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	c := New(t.Context(), Config{URL: srv.URL, TokenURL: srv.URL + "/oauth/v2/token", ClientID: "admin-tenants-client",
		ClientSecret: "wrong", Audience: "p-tm", Timeout: 5 * time.Second}, srv.Client()) // gitleaks:allow
	if _, err := c.List(context.Background(), false); err == nil {
		t.Fatal("no error with refused credentials")
	}
	if len(f.requests) != 0 {
		t.Error("Tenant Management was called without a token")
	}
}
