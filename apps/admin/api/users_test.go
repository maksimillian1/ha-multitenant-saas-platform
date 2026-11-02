package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

func TestListUsersShowsHumansAndMachinesOfEveryOrg(t *testing.T) {
	e := newEnv(t)
	var list UserList
	expect(t, e.do("GET", "/api/users", nil), http.StatusOK, &list)
	if list.Total != 4 || len(list.Items) != 4 {
		t.Fatalf("got %+v", list)
	}
	byID := map[string]User{}
	for _, u := range list.Items {
		byID[u.Id] = u
	}
	ann, ci := byID["ann"], byID["ci"]
	if ann.Type != UserTypeHuman || ann.DisplayName != "Ann Lee" || ann.OrgId != "org-a" || ann.OrgName != "tenant-a" ||
		ann.Human == nil || ann.Machine != nil || ann.Human.Email != "ann@example.test" || !ann.Human.EmailVerified ||
		ann.State != UserStateActive || !slices.Equal(ann.LoginNames, []string{"ann@tenant-a.localtest.me"}) {
		t.Errorf("ann = %+v", ann)
	}
	if ci.Type != UserTypeMachine || ci.DisplayName != "tenant-a API client" || ci.Machine == nil || ci.Human != nil ||
		!ci.Machine.HasSecret || ci.Machine.AccessTokenType != AccessTokenTypeJwt || ci.OrgName != "tenant-a" || ci.LoginNames == nil {
		t.Errorf("ci = %+v", ci)
	}
	if sa := byID["sa"]; sa.Machine.AccessTokenType != AccessTokenTypeBearer || sa.OrgName != "ZITADEL" {
		t.Errorf("service account = %+v", sa)
	}
}

func TestListUsersFilters(t *testing.T) {
	e := newEnv(t)
	var list UserList
	expect(t, e.do("GET", "/api/users?search=tenant&org_id=org-a&type=machine&state=active&offset=0&limit=5", nil), http.StatusOK, &list)
	want := zitadel.UserFilter{Search: "tenant", OrganizationID: "org-a", Type: zitadel.TypeMachine, State: zitadel.UserActive}
	if e.z.userFilter != want || e.z.page != (zitadel.Page{Limit: 5}) {
		t.Fatalf("filter %+v, page %+v", e.z.userFilter, e.z.page)
	}
	if list.Total != 1 || list.Items[0].Id != "ci" {
		t.Fatalf("got %+v", list)
	}
	for query, state := range map[string]zitadel.UserState{
		"inactive": zitadel.UserInactive, "locked": zitadel.UserLocked, "initial": zitadel.UserInitial,
	} {
		expect(t, e.do("GET", "/api/users?type=human&state="+query, nil), http.StatusOK, &list)
		if e.z.userFilter.State != state || e.z.userFilter.Type != zitadel.TypeHuman {
			t.Errorf("state=%s: filter %+v", query, e.z.userFilter)
		}
	}
}

func TestGetUser(t *testing.T) {
	e := newEnv(t)
	var u User
	expect(t, e.do("GET", "/api/users/admin", nil), http.StatusOK, &u)
	if u.Username != "super-admin" || u.OrgName != "ZITADEL" || u.DisplayName != "Super Admin" || u.CreatedAt == nil {
		t.Fatalf("user = %+v", u)
	}
	if rec := e.do("GET", "/api/users/nope", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user: status %d", rec.Code)
	}
}

func TestCreateHumanUser(t *testing.T) {
	e := newEnv(t)
	var made Created
	expect(t, e.do("POST", "/api/users/human", HumanUserCreate{
		OrgId: "org-a", Username: ptr(" bo "), GivenName: " Bo", FamilyName: "Ng ", DisplayName: ptr("Bo N."),
		Email: " bo@example.test ", EmailVerified: ptr(true), Password: ptr(" Secret1! "), PasswordChangeRequired: ptr(true),
	}), http.StatusCreated, &made)
	want := zitadel.NewHumanUser{OrganizationID: "org-a", Username: "bo", GivenName: "Bo", FamilyName: "Ng", DisplayName: "Bo N.",
		Email: "bo@example.test", EmailVerified: true, Password: " Secret1! ", PasswordChangeRequired: true}
	if made.Id == "" || e.z.newHuman != want {
		t.Fatalf("created %+v with %+v, want %+v", made, e.z.newHuman, want)
	}

	// The optional fields can be left out.
	expect(t, e.do("POST", "/api/users/human", HumanUserCreate{OrgId: "org-a", GivenName: "Cy", FamilyName: "Oh", Email: "cy@example.test"}),
		http.StatusCreated, &made)
	if e.z.newHuman != (zitadel.NewHumanUser{OrganizationID: "org-a", GivenName: "Cy", FamilyName: "Oh", Email: "cy@example.test"}) {
		t.Fatalf("created with %+v", e.z.newHuman)
	}

	for _, body := range []HumanUserCreate{
		{GivenName: "A", FamilyName: "B", Email: "a@example.test"},
		{OrgId: "org-a", FamilyName: "B", Email: "a@example.test"},
		{OrgId: "org-a", GivenName: "A", FamilyName: "B", Email: "not-an-email"},
		{OrgId: "org-a", GivenName: "A", FamilyName: "B"},
	} {
		if rec := e.do("POST", "/api/users/human", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%+v: status %d, want 400", body, rec.Code)
		}
	}
	if rec := e.do("POST", "/api/users/human", HumanUserCreate{OrgId: "nope", GivenName: "A", FamilyName: "B", Email: "a@example.test"}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown organization: status %d, want 404", rec.Code)
	}
}

func TestCreateMachineUser(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do("POST", "/api/users/machine", MachineUserCreate{OrgId: "org-a", Username: "ci-2", Name: "CI 2"}), http.StatusCreated, nil)
	if e.z.newMachine != (zitadel.NewMachineUser{OrganizationID: "org-a", Username: "ci-2", Name: "CI 2", AccessTokenType: zitadel.AccessTokenJWT}) {
		t.Fatalf("created with %+v; the default must be JWT, which the API Gateway validates", e.z.newMachine)
	}
	bearer := AccessTokenTypeBearer
	expect(t, e.do("POST", "/api/users/machine", MachineUserCreate{OrgId: "org-a", Username: "ci-3", Name: "CI 3",
		Description: ptr("nightly"), AccessTokenType: &bearer}), http.StatusCreated, nil)
	if e.z.newMachine.AccessTokenType != zitadel.AccessTokenBearer || e.z.newMachine.Description != "nightly" {
		t.Fatalf("created with %+v", e.z.newMachine)
	}
	bad := AccessTokenType("saml")
	for _, body := range []MachineUserCreate{
		{OrgId: "org-a", Name: "no username"},
		{OrgId: "org-a", Username: "no-name"},
		{OrgId: "org-a", Username: "x", Name: "x", AccessTokenType: &bad},
	} {
		if rec := e.do("POST", "/api/users/machine", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%+v: status %d, want 400", body, rec.Code)
		}
	}
}

func TestUpdateUser(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do("PATCH", "/api/users/ann", UserUpdate{Human: &HumanUserUpdate{
		GivenName: ptr(" Anna "), Email: ptr("anna@example.test"), EmailVerified: ptr(true),
	}}), http.StatusNoContent, nil)
	h := e.z.update.Human
	if h == nil || *h.GivenName != "Anna" || *h.Email != "anna@example.test" || !h.EmailVerified ||
		h.FamilyName != nil || h.NickName != nil || e.z.update.Machine != nil || e.z.update.Username != nil {
		t.Fatalf("update = %+v / %+v", e.z.update, h)
	}

	// A machine's description may be cleared.
	expect(t, e.do("PATCH", "/api/users/ci", UserUpdate{Username: ptr("ci-renamed"), Machine: &MachineUserUpdate{Description: ptr("")}}),
		http.StatusNoContent, nil)
	if m := e.z.update.Machine; m == nil || *m.Description != "" || m.Name != nil || *e.z.update.Username != "ci-renamed" {
		t.Fatalf("update = %+v", e.z.update)
	}

	for _, body := range []any{
		UserUpdate{},
		UserUpdate{Human: &HumanUserUpdate{}, Machine: &MachineUserUpdate{}},
		UserUpdate{Human: &HumanUserUpdate{GivenName: ptr(" ")}},
		UserUpdate{Human: &HumanUserUpdate{Email: ptr("no-at")}},
		UserUpdate{Username: ptr("")},
		UserUpdate{Machine: &MachineUserUpdate{Name: ptr("")}},
		`{"human": {"phone": "+41"}}`,
	} {
		if rec := e.do("PATCH", "/api/users/ann", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%+v: status %d, want 400", body, rec.Code)
		}
	}
}

func TestUserActions(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do("POST", "/api/users/ann/password", PasswordSet{Password: "N3w-secret!", ChangeRequired: ptr(true)}), http.StatusNoContent, nil)
	if e.z.password != [2]string{"ann", "N3w-secret!"} || !e.z.changeReq {
		t.Fatalf("password %v, change required %v", e.z.password, e.z.changeReq)
	}
	if rec := e.do("POST", "/api/users/ann/password", PasswordSet{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty password: status %d", rec.Code)
	}

	for _, step := range []struct {
		action string
		state  zitadel.UserState
	}{
		{"lock", zitadel.UserLocked},
		{"unlock", zitadel.UserActive},
		{"deactivate", zitadel.UserInactive},
		{"reactivate", zitadel.UserActive},
	} {
		expect(t, e.do("POST", "/api/users/ann/"+step.action, nil), http.StatusNoContent, nil)
		if e.z.users["ann"].State != step.state {
			t.Fatalf("after %s: state %s", step.action, e.z.users["ann"].State)
		}
	}
	expect(t, e.do("DELETE", "/api/users/ann", nil), http.StatusNoContent, nil)
	if _, ok := e.z.users["ann"]; ok {
		t.Fatal("not deleted")
	}
	if rec := e.do("POST", "/api/users/ann/lock", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted user: status %d", rec.Code)
	}
}

func TestMachineSecret(t *testing.T) {
	e := newEnv(t)
	rec := e.do("POST", "/api/users/ci/secret", nil)
	var secret MachineSecret
	expect(t, rec, http.StatusOK, &secret)
	if secret != (MachineSecret{ClientId: "tenant-a-client", ClientSecret: "secret-of-ci"}) {
		t.Fatalf("secret = %+v", secret)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the secret may be cached")
	}
	expect(t, e.do("DELETE", "/api/users/ci/secret", nil), http.StatusNoContent, nil)
	if e.z.users["ci"].Machine.HasSecret {
		t.Fatal("secret not removed")
	}

	before := len(e.z.called())
	rec = e.do("POST", "/api/users/ann/secret", nil)
	if rec.Code != http.StatusConflict || errorText(t, rec) != "Only machine users have a client secret." {
		t.Fatalf("human: status %d, body %s", rec.Code, rec.Body)
	}
	if slices.Contains(e.z.called()[before:], "AddSecret") {
		t.Fatal("a secret was requested for a human")
	}
}
