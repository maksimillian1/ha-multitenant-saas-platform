package items

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleapp/core"
)

// fakeStore records the tenant of every call and keeps items per tenant.
type fakeStore struct {
	items   map[string]core.Item
	tenants []string
}

func (f *fakeStore) Create(_ context.Context, tenantID, name string) (core.Item, error) {
	f.tenants = append(f.tenants, tenantID)
	it := core.Item{ID: "00000000-0000-0000-0000-000000000001", TenantID: tenantID, Name: name}
	f.items[it.ID] = it
	return it, nil
}

func (f *fakeStore) List(_ context.Context, tenantID string) ([]core.Item, error) {
	f.tenants = append(f.tenants, tenantID)
	return nil, nil
}

func (f *fakeStore) Get(_ context.Context, tenantID, id string) (core.Item, error) {
	f.tenants = append(f.tenants, tenantID)
	it, ok := f.items[id]
	if !ok || it.TenantID != tenantID {
		return core.Item{}, core.ErrNotFound
	}
	return it, nil
}

func (f *fakeStore) Update(ctx context.Context, tenantID, id, name string) (core.Item, error) {
	it, err := f.Get(ctx, tenantID, id)
	it.Name = name
	return it, err
}

func (f *fakeStore) Delete(ctx context.Context, tenantID, id string) error {
	_, err := f.Get(ctx, tenantID, id)
	return err
}

func newTestService() (*Service, *fakeStore) {
	f := &fakeStore{items: map[string]core.Item{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(f, log), f
}

func do(t *testing.T, h http.HandlerFunc, method, id, tenant, body string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/items"
	if id != "" {
		path += "/" + id
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.SetPathValue("id", id)
	if tenant != "" {
		req.Header.Set(core.TenantHeader, tenant)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestRequestWithoutTenantHeaderIsRejected(t *testing.T) {
	svc, f := newTestService()
	for _, tenant := range []string{"", "has space", strings.Repeat("x", 65)} {
		if rec := do(t, svc.ListHandler, http.MethodGet, "", tenant, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("tenant %q: status = %d, want 401", tenant, rec.Code)
		}
	}
	if len(f.tenants) != 0 {
		t.Fatalf("store was called for rejected requests: %v", f.tenants)
	}
}

func TestCreateIgnoresTenantIDInBody(t *testing.T) {
	svc, f := newTestService()
	rec := do(t, svc.CreateHandler, http.MethodPost, "", "tenant-a", `{"name":"n","tenant_id":"tenant-b"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var it core.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &it); err != nil {
		t.Fatal(err)
	}
	if it.TenantID != "tenant-a" || f.tenants[0] != "tenant-a" {
		t.Fatalf("item stored for %q (store called with %v), want tenant-a", it.TenantID, f.tenants)
	}
}

func TestOtherTenantsItemIsNotFound(t *testing.T) {
	svc, _ := newTestService()
	rec := do(t, svc.CreateHandler, http.MethodPost, "", "tenant-b", `{"name":"b"}`)
	var it core.Item
	_ = json.Unmarshal(rec.Body.Bytes(), &it)

	for method, h := range map[string]http.HandlerFunc{
		http.MethodGet:    svc.GetHandler,
		http.MethodPut:    svc.UpdateHandler,
		http.MethodDelete: svc.DeleteHandler,
	} {
		rec := do(t, h, method, it.ID, "tenant-a", `{"name":"hijacked"}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s by tenant-a: status = %d, want 404", method, rec.Code)
		}
	}
}

func TestMalformedItemIDIsNotFound(t *testing.T) {
	svc, f := newTestService()
	if rec := do(t, svc.GetHandler, http.MethodGet, "not-a-uuid", "tenant-a", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if len(f.tenants) != 0 {
		t.Fatal("store was called for a malformed ID")
	}
}

func TestCreateValidatesName(t *testing.T) {
	svc, _ := newTestService()
	for _, body := range []string{`{}`, `{"name":""}`, `not json`, `{"name":"` + strings.Repeat("x", 201) + `"}`} {
		if rec := do(t, svc.CreateHandler, http.MethodPost, "", "tenant-a", body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %.20q: status = %d, want 400", body, rec.Code)
		}
	}
}
