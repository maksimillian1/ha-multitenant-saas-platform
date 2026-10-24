package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleapp/internal/store"
)

// fakeItems records the tenant of every call and keeps items per tenant.
type fakeItems struct {
	items   map[string]store.Item
	tenants []string
}

func (f *fakeItems) Create(_ context.Context, tenantID, name string) (store.Item, error) {
	f.tenants = append(f.tenants, tenantID)
	it := store.Item{ID: "00000000-0000-0000-0000-000000000001", TenantID: tenantID, Name: name}
	f.items[it.ID] = it
	return it, nil
}

func (f *fakeItems) List(_ context.Context, tenantID string) ([]store.Item, error) {
	f.tenants = append(f.tenants, tenantID)
	return nil, nil
}

func (f *fakeItems) Get(_ context.Context, tenantID, id string) (store.Item, error) {
	f.tenants = append(f.tenants, tenantID)
	it, ok := f.items[id]
	if !ok || it.TenantID != tenantID {
		return store.Item{}, store.ErrNotFound
	}
	return it, nil
}

func (f *fakeItems) Update(ctx context.Context, tenantID, id, name string) (store.Item, error) {
	it, err := f.Get(ctx, tenantID, id)
	it.Name = name
	return it, err
}

func (f *fakeItems) Delete(ctx context.Context, tenantID, id string) error {
	_, err := f.Get(ctx, tenantID, id)
	return err
}

func newTestHandler() (http.Handler, *fakeItems) {
	f := &fakeItems{items: map[string]store.Item{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(f, func(context.Context) error { return nil }, log), f
}

func do(t *testing.T, h http.Handler, method, path, tenant, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenant != "" {
		req.Header.Set(TenantHeader, tenant)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRequestWithoutTenantHeaderIsRejected(t *testing.T) {
	h, f := newTestHandler()
	for _, tenant := range []string{"", "has space", strings.Repeat("x", 65)} {
		if rec := do(t, h, http.MethodGet, "/items", tenant, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("tenant %q: status = %d, want 401", tenant, rec.Code)
		}
	}
	if len(f.tenants) != 0 {
		t.Fatalf("store was called for rejected requests: %v", f.tenants)
	}
}

func TestCreateIgnoresTenantIDInBody(t *testing.T) {
	h, f := newTestHandler()
	rec := do(t, h, http.MethodPost, "/items", "tenant-a", `{"name":"n","tenant_id":"tenant-b"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var it store.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &it); err != nil {
		t.Fatal(err)
	}
	if it.TenantID != "tenant-a" || f.tenants[0] != "tenant-a" {
		t.Fatalf("item stored for %q (store called with %v), want tenant-a", it.TenantID, f.tenants)
	}
}

func TestOtherTenantsItemIsNotFound(t *testing.T) {
	h, _ := newTestHandler()
	rec := do(t, h, http.MethodPost, "/items", "tenant-b", `{"name":"b"}`)
	var it store.Item
	_ = json.Unmarshal(rec.Body.Bytes(), &it)

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := do(t, h, method, "/items/"+it.ID, "tenant-a", `{"name":"hijacked"}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s by tenant-a: status = %d, want 404", method, rec.Code)
		}
	}
}

func TestMalformedItemIDIsNotFound(t *testing.T) {
	h, f := newTestHandler()
	if rec := do(t, h, http.MethodGet, "/items/not-a-uuid", "tenant-a", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if len(f.tenants) != 0 {
		t.Fatal("store was called for a malformed ID")
	}
}

func TestCreateValidatesName(t *testing.T) {
	h, _ := newTestHandler()
	for _, body := range []string{`{}`, `{"name":""}`, `not json`, `{"name":"` + strings.Repeat("x", 201) + `"}`} {
		if rec := do(t, h, http.MethodPost, "/items", "tenant-a", body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %.20q: status = %d, want 400", body, rec.Code)
		}
	}
}
