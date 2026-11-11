package api

import (
	"net/http"
	"slices"
	"testing"
)

func TestUserAuthorizations(t *testing.T) {
	e := newEnv(t)
	var list AuthorizationList
	expect(t, e.do("GET", "/api/users/ann/authorizations", nil), http.StatusOK, &list)
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("got %+v", list)
	}
	a := list.Items[0]
	if a.Id != "grant-ann" || a.ProjectName != "platform-admin" || a.ProjectId != "p-admin" || a.OrgName != "ZITADEL" ||
		a.UserId != "ann" || a.UserOrgId != "org-a" || a.State != AuthorizationStateActive ||
		len(a.Roles) != 1 || a.Roles[0].Key != "viewer" || a.CreatedAt == nil {
		t.Fatalf("authorization = %+v", a)
	}
	expect(t, e.do("GET", "/api/users/ci/authorizations", nil), http.StatusOK, &list)
	if list.Total != 0 || list.Items == nil {
		t.Fatalf("no authorizations must be an empty list: %+v", list)
	}
}

func TestAuthorizationLifecycle(t *testing.T) {
	e := newEnv(t)
	var made Created
	// Without org_id the roles are given on behalf of the project's owner.
	expect(t, e.do("POST", "/api/authorizations", AuthorizationCreate{UserId: "ann", ProjectId: "p-api", RoleKeys: []string{"reader", " reader "}}),
		http.StatusCreated, &made)
	if made.Id == "" || e.z.newGrant != [3]string{"ann", "p-api", "org-zitadel"} || !slices.Equal(e.z.roleKeys, []string{"reader"}) {
		t.Fatalf("created %+v with %v %v", made, e.z.newGrant, e.z.roleKeys)
	}
	before := len(e.z.called())
	expect(t, e.do("POST", "/api/authorizations", AuthorizationCreate{UserId: "ann", ProjectId: "p-api", OrgId: ptr("org-a"), RoleKeys: []string{"reader"}}),
		http.StatusCreated, &made)
	if e.z.newGrant[2] != "org-a" {
		t.Fatalf("org_id ignored: %v", e.z.newGrant)
	}
	if calls := e.z.called()[before:]; !slices.Equal(calls, []string{"CreateAuthorization"}) {
		t.Fatalf("calls with org_id: %v, want only CreateAuthorization", calls)
	}

	expect(t, e.do("PATCH", "/api/authorizations/grant-ann", AuthorizationUpdate{RoleKeys: []string{"viewer", "super-admin"}}), http.StatusNoContent, nil)
	if !slices.Equal(e.z.roleKeys, []string{"viewer", "super-admin"}) {
		t.Fatalf("roles = %v", e.z.roleKeys)
	}
	expect(t, e.do("DELETE", "/api/authorizations/grant-ann", nil), http.StatusNoContent, nil)
	if _, ok := e.z.authorizations["grant-ann"]; ok {
		t.Fatal("not deleted")
	}
	if rec := e.do("DELETE", "/api/authorizations/grant-ann", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted assignment: status %d", rec.Code)
	}
}

func TestAuthorizationInputIsChecked(t *testing.T) {
	e := newEnv(t)
	for _, body := range []any{
		AuthorizationCreate{UserId: "ann", ProjectId: "p-api"},
		AuthorizationCreate{UserId: "ann", ProjectId: "p-api", RoleKeys: []string{""}},
		AuthorizationCreate{ProjectId: "p-api", RoleKeys: []string{"reader"}},
		AuthorizationCreate{UserId: "ann", RoleKeys: []string{"reader"}},
	} {
		if rec := e.do("POST", "/api/authorizations", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%+v: status %d, want 400", body, rec.Code)
		}
	}
	if rec := e.do("PATCH", "/api/authorizations/grant-ann", AuthorizationUpdate{}); rec.Code != http.StatusBadRequest {
		t.Errorf("no roles: status %d, want 400", rec.Code)
	}
	if rec := e.do("POST", "/api/authorizations", AuthorizationCreate{UserId: "ann", ProjectId: "nope", RoleKeys: []string{"r"}}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: status %d, want 404", rec.Code)
	}
	if calls := e.z.called(); slices.Contains(calls, "CreateAuthorization") || slices.Contains(calls, "UpdateAuthorization") {
		t.Errorf("invalid input reached Zitadel: %v", calls)
	}
}
