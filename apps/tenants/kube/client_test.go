package kube

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI is a tiny API server: it keeps objects by path, answers 409 on a
// second create and 404 on an unknown path, and lets a namespace linger in
// Terminating for a number of reads after DELETE.
type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	objects  map[string]map[string]any
	requests []string
	linger   int
	fail     map[string]int // path prefix → status to answer with
}

func newFakeAPI(t *testing.T) (*fakeAPI, *Client) {
	t.Helper()
	f := &fakeAPI{t: t, objects: map[string]map[string]any{}, fail: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, func() (string, error) { return "sa-token", nil }, srv.Client(),
		Quota{CPU: "2", Memory: "4Gi", Pods: "10"})
	c.poll = time.Millisecond
	return f, c
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer sa-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	for prefix, status := range f.fail {
		if strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"message":"injected"}`)
			return
		}
	}
	switch r.Method {
	case http.MethodPost:
		var o map[string]any
		_ = json.NewDecoder(r.Body).Decode(&o)
		name := o["metadata"].(map[string]any)["name"].(string)
		path := r.URL.Path + "/" + name
		if _, ok := f.objects[path]; ok {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.objects[path] = o
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(o)
	case http.MethodGet:
		o, ok := f.objects[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if meta := o["metadata"].(map[string]any); meta["deletionTimestamp"] != nil {
			if f.linger == 0 {
				f.removeTree(r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			f.linger--
		}
		_ = json.NewEncoder(w).Encode(o)
	case http.MethodDelete:
		o, ok := f.objects[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		o["metadata"].(map[string]any)["deletionTimestamp"] = "now"
		w.WriteHeader(http.StatusOK)
	}
}

// removeTree drops a namespace and every object in it.
func (f *fakeAPI) removeTree(nsPath string) {
	name := strings.TrimPrefix(nsPath, "/api/v1/namespaces/")
	for p := range f.objects {
		if p == nsPath || strings.Contains(p, "/namespaces/"+name+"/") {
			delete(f.objects, p)
		}
	}
}

func (f *fakeAPI) has(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[path]
	return ok
}

const (
	tenantA = "11111111-1111-4111-8111-111111111111"
	tenantB = "22222222-2222-4222-8222-222222222222"
	nsA     = "/api/v1/namespaces/tenant-" + tenantA
	nsB     = "/api/v1/namespaces/tenant-" + tenantB
	quotaA  = nsA + "/resourcequotas/" + QuotaName
	policyA = "/apis/cilium.io/v2/namespaces/tenant-" + tenantA + "/ciliumnetworkpolicies/" + PolicyName
)

// Verifies: tenant-management AC-2; T-02
func TestEnsureNamespaceCreatesNamespaceQuotaAndPolicy(t *testing.T) {
	f, c := newFakeAPI(t)
	if err := c.EnsureNamespace(context.Background(), tenantA); err != nil {
		t.Fatal(err)
	}
	ns := f.objects[nsA]
	labels := ns["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels[TenantLabel] != tenantA || labels["app.kubernetes.io/managed-by"] != "tenant-management" {
		t.Fatalf("namespace labels = %v", labels)
	}
	hard := f.objects[quotaA]["spec"].(map[string]any)["hard"].(map[string]any)
	if hard["pods"] != "10" || hard["limits.memory"] != "4Gi" || hard["requests.cpu"] != "2" {
		t.Fatalf("quota = %v", hard)
	}
	spec := f.objects[policyA]["spec"].(map[string]any)
	ingress, _ := json.Marshal(spec["ingress"])
	if string(ingress) != `[{"fromEndpoints":[{}]}]` {
		t.Fatalf("policy ingress = %s, want only the same namespace", ingress)
	}
	if sel, _ := json.Marshal(spec["endpointSelector"]); string(sel) != `{}` {
		t.Fatalf("policy selects %s, want every pod of the namespace", sel)
	}
}

// Verifies: tenant-management AC-3; T-05
func TestEnsureNamespaceIsIdempotent(t *testing.T) {
	f, c := newFakeAPI(t)
	ctx := context.Background()
	if err := c.EnsureNamespace(ctx, tenantA); err != nil {
		t.Fatal(err)
	}
	// An earlier attempt stopped after the namespace: quota and policy follow.
	delete(f.objects, quotaA)
	delete(f.objects, policyA)
	if err := c.EnsureNamespace(ctx, tenantA); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureNamespace(ctx, tenantA); err != nil {
		t.Fatal(err)
	}
	if !f.has(quotaA) || !f.has(policyA) {
		t.Fatal("quota or policy missing after a retry")
	}
}

// Verifies: tenant-management AC-7; T-01
func TestNamespaceOfAnotherTenantIsNeverTouched(t *testing.T) {
	f, c := newFakeAPI(t)
	ctx := context.Background()
	if err := c.EnsureNamespace(ctx, tenantB); err != nil {
		t.Fatal(err)
	}
	// A namespace named for A but labelled for B (or not created by us).
	f.objects[nsA] = map[string]any{"metadata": map[string]any{
		"name": "tenant-" + tenantA, "labels": map[string]any{TenantLabel: tenantB, "app.kubernetes.io/managed-by": "tenant-management"},
	}}
	if err := c.EnsureNamespace(ctx, tenantA); !errors.Is(err, ErrNotOurs) {
		t.Fatalf("ensure on a foreign namespace: err = %v", err)
	}
	if err := c.DeleteNamespace(ctx, tenantA); !errors.Is(err, ErrNotOurs) {
		t.Fatalf("delete of a foreign namespace: err = %v", err)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "DELETE") {
			t.Fatalf("sent %s", r)
		}
	}
	if f.has(quotaA) {
		t.Fatal("a quota was put into a foreign namespace")
	}

	// Deleting A's own namespace leaves B's.
	delete(f.objects, nsA)
	if err := c.EnsureNamespace(ctx, tenantA); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteNamespace(ctx, tenantA); err != nil {
		t.Fatal(err)
	}
	if f.has(nsA) || !f.has(nsB) {
		t.Fatalf("after deleting A: A exists %v, B exists %v", f.has(nsA), f.has(nsB))
	}
}

// Verifies: tenant-management AC-7, AC-3; T-05
func TestDeleteNamespaceWaitsUntilGone(t *testing.T) {
	f, c := newFakeAPI(t)
	ctx := context.Background()
	if err := c.EnsureNamespace(ctx, tenantA); err != nil {
		t.Fatal(err)
	}
	f.linger = 3
	if err := c.DeleteNamespace(ctx, tenantA); err != nil {
		t.Fatal(err)
	}
	if f.has(nsA) || f.has(quotaA) || f.has(policyA) {
		t.Fatal("namespace or its objects still exist")
	}
	if err := c.DeleteNamespace(ctx, tenantA); err != nil {
		t.Fatalf("delete again: %v", err)
	}

	// A namespace that stays in Terminating is reported, not waited for forever.
	if err := c.EnsureNamespace(ctx, tenantA); err != nil {
		t.Fatal(err)
	}
	f.linger = 1 << 30
	c.deleteFor = 20 * time.Millisecond
	if err := c.DeleteNamespace(ctx, tenantA); !errors.Is(err, ErrTerminating) {
		t.Fatalf("err = %v, want ErrTerminating", err)
	}
	if err := c.EnsureNamespace(ctx, tenantA); !errors.Is(err, ErrTerminating) {
		t.Fatalf("ensure while terminating: err = %v", err)
	}
}

// Verifies: tenant-management AC-4; T-06
func TestAPIServerErrors(t *testing.T) {
	f, c := newFakeAPI(t)
	f.fail["/apis/cilium.io/"] = http.StatusForbidden
	err := c.EnsureNamespace(context.Background(), tenantA)
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusForbidden || se.Message != "injected" {
		t.Fatalf("err = %#v", err)
	}
	c.token = func() (string, error) { return "", errors.New("no token file") }
	if err := c.DeleteNamespace(context.Background(), tenantA); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("err = %v", err)
	}
}
