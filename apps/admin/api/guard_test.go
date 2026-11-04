package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
)

func ptr[T any](v T) *T { return &v }

// apiRoutes is every route with a request that succeeds for the admin in
// the fake instance.
var apiRoutes = []struct {
	method, path string
	body         any
}{
	{"GET", "/api/overview", nil},
	{"GET", "/api/orgs", nil},
	{"POST", "/api/orgs", OrgCreate{Name: "tenant-c"}},
	{"GET", "/api/orgs/org-a", nil},
	{"PATCH", "/api/orgs/org-a", OrgUpdate{Name: "tenant-a2"}},
	{"DELETE", "/api/orgs/org-b", nil},
	{"POST", "/api/orgs/org-a/deactivate", nil},
	{"POST", "/api/orgs/org-b/activate", nil},
	{"GET", "/api/users", nil},
	{"POST", "/api/users/human", HumanUserCreate{OrgId: "org-a", GivenName: "Bo", FamilyName: "Ng", Email: "bo@example.test"}},
	{"POST", "/api/users/machine", MachineUserCreate{OrgId: "org-a", Username: "ci-2", Name: "CI"}},
	{"GET", "/api/users/ann", nil},
	{"PATCH", "/api/users/ann", UserUpdate{Username: ptr("ann2")}},
	{"DELETE", "/api/users/ann", nil},
	{"POST", "/api/users/ann/password", PasswordSet{Password: "Secret1!"}},
	{"POST", "/api/users/ann/deactivate", nil},
	{"POST", "/api/users/ann/reactivate", nil},
	{"POST", "/api/users/ann/lock", nil},
	{"POST", "/api/users/ann/unlock", nil},
	{"POST", "/api/users/ci/secret", nil},
	{"DELETE", "/api/users/ci/secret", nil},
	{"GET", "/api/users/ann/authorizations", nil},
	{"POST", "/api/authorizations", AuthorizationCreate{UserId: "ann", ProjectId: "p-api", RoleKeys: []string{"reader"}}},
	{"PATCH", "/api/authorizations/grant-ann", AuthorizationUpdate{RoleKeys: []string{"viewer"}}},
	{"DELETE", "/api/authorizations/grant-ann", nil},
	{"GET", "/api/projects", nil},
	{"GET", "/api/projects/p-admin", nil},
	{"GET", "/api/projects/p-admin/roles", nil},
	{"GET", "/api/projects/p-admin/authorizations", nil},

	{"GET", "/api/orgs/org-a/branding", nil},
	{"PUT", "/api/orgs/org-a/branding", brandingInput()},
	{"DELETE", "/api/orgs/org-b/branding", nil},
	{"POST", "/api/orgs/org-b/branding/publish", nil},
	{"GET", "/api/orgs/org-b/branding/assets/logo", nil},
	{"PUT", "/api/orgs/org-a/branding/assets/icon", fileBody(pngFile)},
	{"DELETE", "/api/orgs/org-b/branding/assets/logo", nil},
	{"GET", "/api/platform/branding", nil},
	{"PUT", "/api/platform/branding", brandingInput()},
	{"POST", "/api/platform/branding/publish", nil},
	{"GET", "/api/platform/branding/assets/logo?version=draft", nil},
	{"PUT", "/api/platform/branding/assets/font", fileBody(woffFile)},
	{"DELETE", "/api/platform/branding/assets/logo", nil},
}

func isChange(method string) bool { return method != http.MethodGet }

func TestEveryRouteWorksForTheAdmin(t *testing.T) {
	for _, route := range apiRoutes {
		rec := newEnv(t).do(route.method, route.path, route.body)
		if rec.Code < 200 || rec.Code > 299 {
			t.Errorf("%s %s: status %d, body %s", route.method, route.path, rec.Code, rec.Body)
		}
	}
}

// TestRoutesMatchOpenAPI keeps the hand-written routes and openapi.yaml in
// step (ADR-0009: only the types are generated).
func TestRoutesMatchOpenAPI(t *testing.T) {
	spec, err := os.Open("../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer spec.Close()
	pathLine := regexp.MustCompile(`^  (/api/\S+):$`)
	methodLine := regexp.MustCompile(`^    (get|post|put|patch|delete):$`)
	var documented []string
	var path string
	for scanner := bufio.NewScanner(spec); scanner.Scan(); {
		if m := pathLine.FindStringSubmatch(scanner.Text()); m != nil {
			path = m[1]
		} else if m := methodLine.FindStringSubmatch(scanner.Text()); m != nil {
			documented = append(documented, strings.ToUpper(m[1])+" "+path)
		}
	}

	e := newEnv(t)
	tested := map[string]bool{}
	for _, route := range apiRoutes {
		_, pattern := e.mux.Handler(httptest.NewRequest(route.method, route.path, nil))
		tested[pattern] = true
	}
	for _, op := range documented {
		method, path, _ := strings.Cut(op, " ")
		concrete := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, "x1")
		_, pattern := e.mux.Handler(httptest.NewRequest(method, concrete, nil))
		if pattern != op {
			t.Errorf("%s is documented but routed to %q", op, pattern)
		}
		if !tested[op] && op != "GET /api/me" {
			t.Errorf("%s has no entry in apiRoutes", op)
		}
	}
	if len(tested) != len(documented)-1 { // apiRoutes leaves out /api/me
		t.Errorf("%d routes tested, %d documented", len(tested), len(documented))
	}
}

func TestEveryRouteRequiresAdmin(t *testing.T) {
	viewer := fakeSessions{ok: true, session: core.Session{Subject: "viewer", Roles: []string{"viewer"}}}
	for _, tc := range []struct {
		name     string
		sessions fakeSessions
		status   int
	}{
		{"no session", fakeSessions{}, http.StatusUnauthorized},
		{"no admin role", viewer, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, route := range apiRoutes {
				e := newEnvWith(t, tc.sessions)
				rec := e.do(route.method, route.path, route.body)
				if rec.Code != tc.status {
					t.Errorf("%s %s: status %d, want %d", route.method, route.path, rec.Code, tc.status)
				}
				errorText(t, rec)
				if calls := e.z.called(); len(calls) > 0 {
					t.Errorf("%s %s called Zitadel: %v", route.method, route.path, calls)
				}
			}
		})
	}
}

func TestMutationsNeedCSRFHeader(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers [][2]string
	}{
		{"no X-Requested-With", [][2]string{{"X-Requested-With", ""}}},
		{"cross-site fetch", [][2]string{{"Sec-Fetch-Site", "cross-site"}, {"Origin", "http://evil.test"}}},
		{"same-site but other origin", [][2]string{{"Sec-Fetch-Site", "same-site"}, {"Origin", "http://evil.admin.test"}}},
		{"foreign Origin without Sec-Fetch-Site", [][2]string{{"Sec-Fetch-Site", ""}, {"Origin", "http://evil.test"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, route := range apiRoutes {
				if !isChange(route.method) {
					continue
				}
				e := newEnv(t)
				rec := e.do(route.method, route.path, route.body, tc.headers...)
				if rec.Code != http.StatusForbidden || errorText(t, rec) != "cross-site request refused" {
					t.Errorf("%s %s: status %d, body %s; want 403", route.method, route.path, rec.Code, rec.Body)
				}
				if calls := e.z.called(); len(calls) > 0 {
					t.Errorf("%s %s called Zitadel: %v", route.method, route.path, calls)
				}
			}
		})
	}

	// Reads need no header: a cross-site page cannot read the answer.
	if rec := newEnv(t).do("GET", "/api/orgs", nil, [2]string{"X-Requested-With", ""}); rec.Code != http.StatusOK {
		t.Errorf("GET without X-Requested-With: status %d", rec.Code)
	}
	// Without Origin and Sec-Fetch-Site (not a browser) the header is enough.
	rec := newEnv(t).do("POST", "/api/orgs", OrgCreate{Name: "x"}, [2]string{"Origin", ""}, [2]string{"Sec-Fetch-Site", ""})
	if rec.Code != http.StatusCreated {
		t.Errorf("POST with only X-Requested-With: status %d", rec.Code)
	}
}

func TestSelfLockoutIsRefused(t *testing.T) {
	mutations := []string{"LockUser", "DeactivateUser", "DeleteUser", "DeactivateOrganization", "DeleteOrganization",
		"UpdateAuthorization", "DeleteAuthorization"}
	for _, tc := range []struct {
		method, path string
		body         any
		message      string
	}{
		{"POST", "/api/users/admin/lock", nil, "You cannot lock your own user."},
		{"POST", "/api/users/admin/deactivate", nil, "You cannot deactivate your own user."},
		{"DELETE", "/api/users/admin", nil, "You cannot delete your own user."},
		{"POST", "/api/users/sa/lock", nil, "You cannot lock the admin app's service account."},
		{"POST", "/api/users/sa/deactivate", nil, "You cannot deactivate the admin app's service account."},
		{"DELETE", "/api/users/sa", nil, "You cannot delete the admin app's service account."},
		{"POST", "/api/orgs/org-zitadel/deactivate", nil, "You cannot deactivate your own organization."},
		{"DELETE", "/api/orgs/org-zitadel", nil, "You cannot delete your own organization."},
		{"PATCH", "/api/authorizations/grant-admin", AuthorizationUpdate{RoleKeys: []string{"viewer"}}, "You cannot change your own role assignments."},
		{"DELETE", "/api/authorizations/grant-admin", nil, "You cannot change your own role assignments."},
	} {
		e := newEnv(t)
		rec := e.do(tc.method, tc.path, tc.body)
		if rec.Code != http.StatusConflict || errorText(t, rec) != tc.message {
			t.Errorf("%s %s: status %d, body %s; want 409 %q", tc.method, tc.path, rec.Code, rec.Body, tc.message)
		}
		for _, call := range e.z.called() {
			if slices.Contains(mutations, call) {
				t.Errorf("%s %s sent %s to Zitadel", tc.method, tc.path, call)
			}
		}
	}

	// The service account's organization, when it is not the admin's.
	e := newEnv(t)
	e.z.account.OrganizationID = "org-a"
	rec := e.do("DELETE", "/api/orgs/org-a", nil)
	if rec.Code != http.StatusConflict || !strings.Contains(errorText(t, rec), "service account") {
		t.Errorf("deleting the service account's organization: status %d, body %s", rec.Code, rec.Body)
	}
	if slices.Contains(e.z.called(), "DeleteOrganization") {
		t.Error("the service account's organization was deleted")
	}

	// Other users, organizations and role assignments can be changed.
	e = newEnv(t)
	expect(t, e.do("POST", "/api/users/ann/lock", nil), http.StatusNoContent, nil)
	expect(t, e.do("POST", "/api/orgs/org-a/deactivate", nil), http.StatusNoContent, nil)
	expect(t, e.do("DELETE", "/api/authorizations/grant-ann", nil), http.StatusNoContent, nil)
}

func TestMutationsAreLogged(t *testing.T) {
	e := newEnv(t)
	var org Created
	expect(t, e.do("POST", "/api/orgs", OrgCreate{Name: "tenant-c"}), http.StatusCreated, &org)
	expect(t, e.do("POST", "/api/users/ann/lock", nil), http.StatusNoContent, nil)
	expect(t, e.do("DELETE", "/api/users/admin", nil), http.StatusConflict, nil)
	expect(t, e.do("GET", "/api/users/ann", nil), http.StatusOK, nil)

	type entry struct {
		Msg, Actor, Action, Target string
		Status                     int
	}
	var changes []entry
	for line := range strings.SplitSeq(strings.TrimSpace(e.logs.String()), "\n") {
		var got entry
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if got.Msg == "admin change" {
			changes = append(changes, got)
		}
	}
	want := []entry{
		{"admin change", "admin", "POST /api/orgs", org.Id, http.StatusCreated},
		{"admin change", "admin", "POST /api/users/{userId}/lock", "ann", http.StatusNoContent},
		{"admin change", "admin", "DELETE /api/users/{userId}", "admin", http.StatusConflict},
	}
	if !slices.Equal(changes, want) {
		t.Fatalf("change log\n%+v\nwant\n%+v", changes, want)
	}
}
