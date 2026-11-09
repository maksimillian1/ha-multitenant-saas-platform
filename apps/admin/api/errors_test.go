package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

func zerr(status int, code, message string) error {
	return fmt.Errorf("zitadel: %w", &zitadel.Error{Status: status, Code: code, Message: message})
}

func TestZitadelErrorsAreMapped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"not found", zerr(404, zitadel.CodeNotFound, "Organisation not found (QUERY-iTTGJ)"), 404, "Organisation not found (QUERY-iTTGJ)"},
		{"invalid argument", zerr(400, zitadel.CodeInvalidArgument, "Password is too short (COMMA-HuJf6)"), 400, "Password is too short (COMMA-HuJf6)"},
		{"already exists", zerr(409, zitadel.CodeAlreadyExists, "Organisation already exists (V2-HN3SD)"), 409, "Organisation already exists (V2-HN3SD)"},
		{"failed precondition", zerr(400, zitadel.CodeFailedPrecondition, "User is already locked (COMMAND-3NN8v)"), 409, "User is already locked (COMMAND-3NN8v)"},
		{"service account token rejected", zerr(401, zitadel.CodeUnauthenticated, "Token invalid (AUTH-7fs1e)"), 502, "Zitadel refused the admin app's service account"},
		{"service account lacks a permission", zerr(403, zitadel.CodePermissionDenied, "No matching permissions found (AUTH-5mWD2)"), 502, "Zitadel refused the admin app's service account"},
		{"Zitadel unavailable", zerr(503, zitadel.CodeUnavailable, "Service Unavailable"), 502, "Zitadel is unavailable"},
		{"Zitadel deadline", zerr(504, zitadel.CodeDeadlineExceeded, "deadline exceeded"), 504, "Zitadel did not answer in time"},
		{"client timeout", fmt.Errorf("zitadel: %w", context.DeadlineExceeded), 504, "Zitadel did not answer in time"},
		{"connection refused", errors.New("zitadel: dial tcp 10.0.0.1:8080: connect: connection refused"), 502, "Zitadel is unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.z.err = tc.err
			rec := e.do("GET", "/api/orgs", nil)
			if rec.Code != tc.status || errorText(t, rec) != tc.body {
				t.Fatalf("status %d, body %s; want %d %q", rec.Code, rec.Body, tc.status, tc.body)
			}
			if strings.Contains(rec.Body.String(), "AUTH-") || strings.Contains(rec.Body.String(), "10.0.0.1") {
				t.Errorf("the answer shows Zitadel internals: %s", rec.Body)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Error("an error answer may be cached")
			}
		})
	}
}

func TestBadRequestsNeverReachZitadel(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/tenants", `{"name": "x"`},
		{"POST", "/api/tenants", `{"name": "x", "admin_email": "a@b.test", "admins": []}`},
		{"POST", "/api/tenants", `{"name": "x", "admin_email": "a@b.test"} {"name": "y"}`},
		{"POST", "/api/tenants", TenantCreate{Name: "   ", AdminEmail: "a@b.test"}},
		{"POST", "/api/tenants", TenantCreate{Name: strings.Repeat("n", 101), AdminEmail: "a@b.test"}},
		{"POST", "/api/tenants", TenantCreate{Name: "x", AdminEmail: "not-an-email"}},
		{"GET", "/api/tenants?include_deleted=yes", nil},
		{"PATCH", "/api/orgs/org-a", OrgUpdate{Name: strings.Repeat("n", 201)}},
		{"GET", "/api/orgs?limit=0", nil},
		{"GET", "/api/orgs?limit=101", nil},
		{"GET", "/api/orgs?offset=-1", nil},
		{"GET", "/api/orgs?state=removed", nil},
		{"GET", "/api/orgs?search=" + strings.Repeat("s", 201), nil},
		{"GET", "/api/users?type=robot", nil},
		{"GET", "/api/users?state=deleted", nil},
		{"GET", "/api/projects?limit=x", nil},
	} {
		e := newEnv(t)
		rec := e.do(tc.method, tc.path, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status %d, body %s; want 400", tc.method, tc.path, rec.Code, rec.Body)
		}
		errorText(t, rec)
		if calls := e.z.called(); len(calls) > 0 {
			t.Errorf("%s %s called Zitadel: %v", tc.method, tc.path, calls)
		}
		if calls := e.tm.called(); len(calls) > 0 {
			t.Errorf("%s %s called Tenant Management: %v", tc.method, tc.path, calls)
		}
	}
}
