package api

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/tenants"
)

// fakeTenants is an in-memory Tenant Management with its state rules. It
// records every call with the admin who made it, so a test can check that
// nothing reached it and who was named.
type fakeTenants struct {
	mu      sync.Mutex
	calls   []string // "Method admin id"
	err     error    // every call fails with it when set
	tenants map[string]*tenants.Tenant
	nextID  int
}

// newFakeTenants holds three tenants: active, suspended and one whose
// provisioning stopped after the organization step.
func newFakeTenants() *fakeTenants {
	f := &fakeTenants{tenants: map[string]*tenants.Tenant{}}
	for _, t := range []tenants.Tenant{
		{ID: "t-active", Name: "Acme", AdminEmail: "it@acme.test", State: "active", Parts: []string{"organization", "admin", "namespace"}},
		{ID: "t-suspended", Name: "Globex", AdminEmail: "ops@globex.test", State: "suspended", Parts: []string{"organization", "admin", "namespace"}},
		{ID: "t-prov", Name: "Initech", AdminEmail: "it@initech.test", State: "provisioning", Parts: []string{"organization"},
			LastError: "admin: zitadel: unavailable"},
	} {
		t.CreatedBy, t.CreatedAt, t.UpdatedAt = "admin", created1, created1
		f.tenants[t.ID] = &t
	}
	return f
}

func (f *fakeTenants) record(method, admin, id string) error {
	f.calls = append(f.calls, strings.Join(slices.DeleteFunc([]string{method, admin, id}, func(s string) bool { return s == "" }), " "))
	return f.err
}

func (f *fakeTenants) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func tenantErr(status int, msg string, t *tenants.Tenant) error {
	var copied *tenants.Tenant
	if t != nil {
		c := *t
		copied = &c
	}
	return &tenants.Error{Status: status, Message: msg, Tenant: copied}
}

func (f *fakeTenants) List(_ context.Context, includeDeleted bool) ([]tenants.Tenant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("List", "", strconv.FormatBool(includeDeleted)); err != nil {
		return nil, err
	}
	var out []tenants.Tenant
	for _, t := range f.tenants {
		if t.State != "deleted" || includeDeleted {
			out = append(out, *t)
		}
	}
	slices.SortFunc(out, func(a, b tenants.Tenant) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (f *fakeTenants) Get(_ context.Context, id string) (tenants.Tenant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Get", "", id); err != nil {
		return tenants.Tenant{}, err
	}
	t, ok := f.tenants[id]
	if !ok {
		return tenants.Tenant{}, tenantErr(http.StatusNotFound, "tenant not found", nil)
	}
	return *t, nil
}

func (f *fakeTenants) Create(_ context.Context, admin string, in tenants.Create) (tenants.Tenant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Create", admin, in.Name+" "+in.AdminEmail); err != nil {
		return tenants.Tenant{}, err
	}
	for _, t := range f.tenants {
		if t.State != "deleted" && strings.EqualFold(t.Name, in.Name) {
			return tenants.Tenant{}, tenantErr(http.StatusConflict, "a tenant with this name exists", nil)
		}
	}
	f.nextID++
	now := time.Now().UTC()
	t := &tenants.Tenant{ID: "t-new-" + strconv.Itoa(f.nextID), Name: in.Name, AdminEmail: in.AdminEmail, State: "active",
		Parts: []string{"organization", "admin", "namespace"}, CreatedBy: admin, CreatedAt: now, UpdatedAt: now}
	f.tenants[t.ID] = t
	return *t, nil
}

// change applies a state change allowed only from the states in from.
func (f *fakeTenants) change(method, admin, id string, from []string, apply func(*tenants.Tenant)) (tenants.Tenant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(method, admin, id); err != nil {
		return tenants.Tenant{}, err
	}
	t, ok := f.tenants[id]
	if !ok {
		return tenants.Tenant{}, tenantErr(http.StatusNotFound, "tenant not found", nil)
	}
	if !slices.Contains(from, t.State) {
		return tenants.Tenant{}, tenantErr(http.StatusConflict, "the tenant is "+t.State, t)
	}
	apply(t)
	t.UpdatedAt = time.Now().UTC()
	return *t, nil
}

func (f *fakeTenants) Retry(_ context.Context, admin, id string) (tenants.Tenant, error) {
	return f.change("Retry", admin, id, []string{"provisioning", "deleting"}, func(t *tenants.Tenant) {
		t.LastError = ""
		if t.State == "deleting" {
			tombstone(t, admin)
			return
		}
		t.State, t.Parts = "active", []string{"organization", "admin", "namespace"}
	})
}

func (f *fakeTenants) Suspend(_ context.Context, admin, id string) (tenants.Tenant, error) {
	return f.change("Suspend", admin, id, []string{"active"}, func(t *tenants.Tenant) { t.State = "suspended" })
}

func (f *fakeTenants) Resume(_ context.Context, admin, id string) (tenants.Tenant, error) {
	return f.change("Resume", admin, id, []string{"suspended"}, func(t *tenants.Tenant) { t.State = "active" })
}

func (f *fakeTenants) Delete(_ context.Context, admin, id string) (tenants.Tenant, error) {
	return f.change("Delete", admin, id, []string{"suspended", "provisioning", "deleting"}, func(t *tenants.Tenant) { tombstone(t, admin) })
}

func tombstone(t *tenants.Tenant, admin string) {
	now := time.Now().UTC()
	*t = tenants.Tenant{ID: t.ID, State: "deleted", Parts: []string{}, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt,
		DeletedBy: admin, DeletedAt: &now}
}
