package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/tenants"
)

// TestTenantRoutes: the tenant routes call Tenant Management with the
// admin's user ID and map its answers. The session and CSRF checks of every
// tenant route are in TestEveryRouteRequiresAdmin and
// TestMutationsNeedCSRFHeader (apiRoutes lists them).
//
// Verifies: tenant-management AC-14; T-04
func TestTenantRoutes(t *testing.T) {
	e := newEnv(t)

	var made Tenant
	expect(t, e.do("POST", "/api/tenants", TenantCreate{Name: "  Umbrella ", AdminEmail: " it@umbrella.test "}), http.StatusCreated, &made)
	if made.Name == nil || *made.Name != "Umbrella" || made.State != TenantStateActive || len(made.Parts) != 3 || made.CreatedBy != "admin" {
		t.Fatalf("created %+v", made)
	}

	var list TenantList
	expect(t, e.do("GET", "/api/tenants", nil), http.StatusOK, &list)
	if len(list.Items) != 4 {
		t.Fatalf("list = %+v", list)
	}
	var prov Tenant
	expect(t, e.do("GET", "/api/tenants/t-prov", nil), http.StatusOK, &prov)
	if prov.State != TenantStateProvisioning || prov.LastError == nil || !slices.Equal(prov.Parts, []TenantPart{TenantPartOrganization}) {
		t.Fatalf("t-prov = %+v", prov)
	}

	var after Tenant
	expect(t, e.do("POST", "/api/tenants/t-prov/retry", nil), http.StatusOK, &after)
	if after.State != TenantStateActive || after.LastError != nil {
		t.Errorf("after retry: %+v", after)
	}
	expect(t, e.do("POST", "/api/tenants/t-active/suspend", nil), http.StatusOK, &after)
	expect(t, e.do("POST", "/api/tenants/t-active/resume", nil), http.StatusOK, &after)
	expect(t, e.do("POST", "/api/tenants/t-active/suspend", nil), http.StatusOK, &after)
	var tomb Tenant
	expect(t, e.do("DELETE", "/api/tenants/t-active", nil), http.StatusOK, &tomb)
	if tomb.State != TenantStateDeleted || tomb.Name != nil || tomb.AdminEmail != nil || tomb.DeletedBy == nil || *tomb.DeletedBy != "admin" {
		t.Errorf("tombstone %+v", tomb)
	}
	expect(t, e.do("GET", "/api/tenants?include_deleted=true", nil), http.StatusOK, &list)
	if len(list.Items) != 4 {
		t.Errorf("with tombstones: %d tenants, want 4", len(list.Items))
	}

	want := []string{
		"Create admin Umbrella it@umbrella.test",
		"List false",
		"Get t-prov",
		"Retry admin t-prov",
		"Suspend admin t-active",
		"Resume admin t-active",
		"Suspend admin t-active",
		"Delete admin t-active",
		"List true",
	}
	if got := e.tm.called(); !slices.Equal(got, want) {
		t.Errorf("calls\n%q\nwant\n%q", got, want)
	}
	if calls := e.z.called(); len(calls) > 0 {
		t.Errorf("tenant routes called Zitadel directly: %v", calls)
	}
}

func TestTenantErrorsAreMapped(t *testing.T) {
	failed := &tenants.Tenant{ID: "t-prov", Name: "Initech", State: "provisioning", Parts: []string{"organization"},
		LastError: "admin: zitadel: unavailable", CreatedBy: "admin", CreatedAt: created1, UpdatedAt: created1}
	for _, tc := range []struct {
		name       string
		err        error
		status     int
		message    string
		withTenant bool
	}{
		{"bad request passes through", &tenants.Error{Status: 400, Message: "admin_email is not an email address"}, 400, "admin_email is not an email address", false},
		{"not found passes through", &tenants.Error{Status: 404, Message: "tenant not found"}, 404, "tenant not found", false},
		{"conflict with the tenant", &tenants.Error{Status: 409, Message: "the tenant is busy", Tenant: failed}, 409, "the tenant is busy", true},
		{"failed step with the tenant", &tenants.Error{Status: 502, Message: "step admin: unavailable", Tenant: failed}, 502, "step admin: unavailable", true},
		{"timed-out step with the tenant", &tenants.Error{Status: 504, Message: "step admin: timeout", Tenant: failed}, 504, "step admin: timeout", true},
		{"token refused", &tenants.Error{Status: 401, Message: "invalid token"}, 502, "Tenant Management refused the admin app's machine token", false},
		{"role missing", &tenants.Error{Status: 403, Message: "missing role"}, 502, "Tenant Management refused the admin app's machine token", false},
		{"other answer", &tenants.Error{Status: 500, Message: "boom"}, 502, "Tenant Management failed", false},
		{"unreachable", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, 502, "Tenant Management is unavailable", false},
		{"too slow", context.DeadlineExceeded, 504, "Tenant Management did not answer in time", false},
		{"network timeout", timeoutErr{}, 504, "Tenant Management did not answer in time", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.tm.err = tc.err
			rec := e.do("POST", "/api/tenants/t-prov/retry", nil)
			var body TenantError
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body %s: %v", rec.Body, err)
			}
			if rec.Code != tc.status || body.Error != tc.message {
				t.Fatalf("status %d, body %s; want %d %q", rec.Code, rec.Body, tc.status, tc.message)
			}
			if tc.withTenant != (body.Tenant != nil) {
				t.Fatalf("tenant in the answer: %v, want %v", body.Tenant != nil, tc.withTenant)
			}
			if tc.withTenant && (body.Tenant.Id != "t-prov" || body.Tenant.LastError == nil) {
				t.Errorf("tenant %+v", body.Tenant)
			}
		})
	}
}

func TestTenantRoutesWithoutTenantManagement(t *testing.T) {
	logs := io.Discard
	svc := NewService(adminSession(), newFakeZitadel(), nil, "super-admin", testOrigin+"/", slog.New(slog.NewJSONHandler(logs, nil)))
	mux := http.NewServeMux()
	svc.Register(mux)
	e := &env{t: t, mux: mux}
	for _, route := range apiRoutes {
		if len(route.path) < len("/api/tenants") || route.path[:len("/api/tenants")] != "/api/tenants" {
			continue
		}
		if rec := e.do(route.method, route.path, route.body); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: status %d, want 503", route.method, route.path, rec.Code)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }
