package zitadel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// fakeZitadel answers every request with the canned body for its path and
// records what it got. The expected request bodies in these tests are the
// protojson form of the v4.19.2 messages (proto/zitadel/*/v2).
type fakeZitadel struct {
	t        *testing.T
	answers  map[string]string // path → 200 body
	requests []recorded
}

type recorded struct {
	method, path string
	header       http.Header
	body         any
}

func newFake(t *testing.T, answers map[string]string) (*fakeZitadel, *Client) {
	t.Helper()
	f := &fakeZitadel{t: t, answers: answers}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, NewClient(srv.URL+"/", staticTokens("token-of-admin-app"), srv.Client())
}

func (f *fakeZitadel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			f.t.Errorf("%s: body is not JSON: %s", r.URL.Path, raw)
		}
	}
	f.requests = append(f.requests, recorded{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: body})
	answer, ok := f.answers[r.URL.Path]
	if !ok {
		answer = "{}"
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, answer)
}

func (f *fakeZitadel) last() recorded {
	f.t.Helper()
	if len(f.requests) == 0 {
		f.t.Fatal("no request reached Zitadel")
	}
	return f.requests[len(f.requests)-1]
}

func jsonValue(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad expected JSON %s: %v", s, err)
	}
	return v
}

func TestClientSendsConnectJSONWithToken(t *testing.T) {
	f, c := newFake(t, nil)
	if _, err := c.ListOrganizations(context.Background(), OrganizationFilter{}, Page{Limit: 5}); err != nil {
		t.Fatal(err)
	}
	got := f.last()
	if got.method != http.MethodPost || got.path != "/zitadel.org.v2.OrganizationService/ListOrganizations" {
		t.Fatalf("%s %s, want POST /zitadel.org.v2.OrganizationService/ListOrganizations", got.method, got.path)
	}
	for header, want := range map[string]string{
		"Content-Type":             "application/json",
		"Connect-Protocol-Version": "1",
		"Authorization":            "Bearer token-of-admin-app",
	} {
		if got.header.Get(header) != want {
			t.Errorf("%s = %q, want %q", header, got.header.Get(header), want)
		}
	}
}

// TestRequestBodies checks the path and the JSON body of every call against
// the v4.19.2 protos.
func TestRequestBodies(t *testing.T) {
	ctx := context.Background()
	const org = "/zitadel.org.v2.OrganizationService/"
	const user = "/zitadel.user.v2.UserService/"
	const authz = "/zitadel.authorization.v2.AuthorizationService/"
	const project = "/zitadel.project.v2.ProjectService/"
	name := "Renamed"
	nick := "ally"

	for _, tc := range []struct {
		name string
		call func(c *Client) error
		path string
		body string
	}{
		{"list organizations with every filter", func(c *Client) error {
			_, err := c.ListOrganizations(ctx, OrganizationFilter{Name: "ten", State: OrganizationActive, ID: "1", Default: true}, Page{Offset: 40, Limit: 20})
			return err
		}, org + "ListOrganizations", `{"query": {"offset": 40, "limit": 20, "asc": true}, "sortingColumn": "ORGANIZATION_FIELD_NAME_NAME",
			"queries": [{"nameQuery": {"name": "ten", "method": "TEXT_QUERY_METHOD_CONTAINS_IGNORE_CASE"}},
			            {"stateQuery": {"state": "ORGANIZATION_STATE_ACTIVE"}}, {"idQuery": {"id": "1"}}, {"defaultQuery": {}}]}`},
		{"add organization", func(c *Client) error { _, err := c.AddOrganization(ctx, "tenant-c"); return err },
			org + "AddOrganization", `{"name": "tenant-c"}`},
		{"update organization", func(c *Client) error { return c.UpdateOrganization(ctx, "1", "tenant-d") },
			org + "UpdateOrganization", `{"organizationId": "1", "name": "tenant-d"}`},
		{"deactivate organization", func(c *Client) error { return c.DeactivateOrganization(ctx, "1") },
			org + "DeactivateOrganization", `{"organizationId": "1"}`},
		{"activate organization", func(c *Client) error { return c.ActivateOrganization(ctx, "1") },
			org + "ActivateOrganization", `{"organizationId": "1"}`},
		{"delete organization", func(c *Client) error { return c.DeleteOrganization(ctx, "1") },
			org + "DeleteOrganization", `{"organizationId": "1"}`},

		{"list users with every filter", func(c *Client) error {
			_, err := c.ListUsers(ctx, UserFilter{Search: "ann", OrganizationID: "1", Type: TypeHuman, State: UserLocked}, Page{Limit: 20})
			return err
		}, user + "ListUsers", `{"query": {"limit": 20, "asc": true}, "sortingColumn": "USER_FIELD_NAME_USER_NAME", "queries": [
			{"orQuery": {"queries": [
				{"userNameQuery": {"userName": "ann", "method": "TEXT_QUERY_METHOD_CONTAINS_IGNORE_CASE"}},
				{"displayNameQuery": {"displayName": "ann", "method": "TEXT_QUERY_METHOD_CONTAINS_IGNORE_CASE"}},
				{"emailQuery": {"emailAddress": "ann", "method": "TEXT_QUERY_METHOD_CONTAINS_IGNORE_CASE"}}]}},
			{"organizationIdQuery": {"organizationId": "1"}}, {"typeQuery": {"type": "TYPE_HUMAN"}}, {"stateQuery": {"state": "USER_STATE_LOCKED"}}]}`},
		{"list users without filters", func(c *Client) error { _, err := c.ListUsers(ctx, UserFilter{}, Page{}); return err },
			user + "ListUsers", `{"query": {"asc": true}, "sortingColumn": "USER_FIELD_NAME_USER_NAME"}`},
		{"get user", func(c *Client) error {
			_, err := c.GetUser(ctx, "7")
			if HasCode(err, CodeNotFound) { // the fake answers {}
				err = nil
			}
			return err
		}, user + "GetUserByID", `{"userId": "7"}`},
		{"create a verified human with a password", func(c *Client) error {
			_, err := c.CreateHumanUser(ctx, NewHumanUser{OrganizationID: "1", Username: "ann", GivenName: "Ann", FamilyName: "Lee",
				DisplayName: "Ann Lee", Email: "ann@a.test", EmailVerified: true, Password: "Secret1!", PasswordChangeRequired: true})
			return err
		}, user + "CreateUser", `{"organizationId": "1", "username": "ann", "human": {
			"profile": {"givenName": "Ann", "familyName": "Lee", "displayName": "Ann Lee"},
			"email": {"email": "ann@a.test", "isVerified": true},
			"password": {"password": "Secret1!", "changeRequired": true}}}`},
		{"create an unverified human without a password", func(c *Client) error {
			_, err := c.CreateHumanUser(ctx, NewHumanUser{OrganizationID: "1", GivenName: "Bo", FamilyName: "Ng", Email: "bo@a.test"})
			return err
		}, user + "CreateUser", `{"organizationId": "1", "human": {"profile": {"givenName": "Bo", "familyName": "Ng"}, "email": {"email": "bo@a.test"}}}`},
		{"create a machine", func(c *Client) error {
			_, err := c.CreateMachineUser(ctx, NewMachineUser{OrganizationID: "1", Username: "ci", Name: "CI", AccessTokenType: AccessTokenJWT})
			return err
		}, user + "CreateUser", `{"organizationId": "1", "username": "ci", "machine": {"name": "CI", "accessTokenType": "ACCESS_TOKEN_TYPE_JWT"}}`},
		{"update a human", func(c *Client) error {
			mail := "new@a.test"
			return c.UpdateUser(ctx, "7", UserUpdate{Username: &nick, Human: &HumanUpdate{NickName: &nick, Email: &mail, EmailVerified: true}})
		}, user + "UpdateUser", `{"userId": "7", "username": "ally", "human": {"profile": {"nickName": "ally"}, "email": {"email": "new@a.test", "isVerified": true}}}`},
		{"update a machine", func(c *Client) error {
			empty := ""
			return c.UpdateUser(ctx, "8", UserUpdate{Machine: &MachineUpdate{Name: &name, Description: &empty}})
		}, user + "UpdateUser", `{"userId": "8", "machine": {"name": "Renamed", "description": ""}}`},
		{"set a password", func(c *Client) error { return c.SetPassword(ctx, "7", "Secret2!", false) },
			user + "UpdateUser", `{"userId": "7", "human": {"password": {"password": {"password": "Secret2!"}}}}`},
		{"deactivate user", func(c *Client) error { return c.DeactivateUser(ctx, "7") }, user + "DeactivateUser", `{"userId": "7"}`},
		{"reactivate user", func(c *Client) error { return c.ReactivateUser(ctx, "7") }, user + "ReactivateUser", `{"userId": "7"}`},
		{"lock user", func(c *Client) error { return c.LockUser(ctx, "7") }, user + "LockUser", `{"userId": "7"}`},
		{"unlock user", func(c *Client) error { return c.UnlockUser(ctx, "7") }, user + "UnlockUser", `{"userId": "7"}`},
		{"delete user", func(c *Client) error { return c.DeleteUser(ctx, "7") }, user + "DeleteUser", `{"userId": "7"}`},
		{"add secret", func(c *Client) error { _, err := c.AddSecret(ctx, "8"); return err }, user + "AddSecret", `{"userId": "8"}`},
		{"remove secret", func(c *Client) error { return c.RemoveSecret(ctx, "8") }, user + "RemoveSecret", `{"userId": "8"}`},

		{"list authorizations with every filter", func(c *Client) error {
			_, err := c.ListAuthorizations(ctx, AuthorizationFilter{IDs: []string{"a1"}, UserID: "7", ProjectID: "p1"}, Page{Limit: 100})
			return err
		}, authz + "ListAuthorizations", `{"pagination": {"limit": 100, "asc": true}, "sortingColumn": "AUTHORIZATION_FIELD_NAME_CREATED_DATE",
			"filters": [{"authorizationIds": {"ids": ["a1"]}}, {"inUserIds": {"ids": ["7"]}}, {"projectId": {"id": "p1"}}]}`},
		{"create authorization", func(c *Client) error {
			_, err := c.CreateAuthorization(ctx, "7", "p1", "1", []string{"viewer"})
			return err
		}, authz + "CreateAuthorization", `{"userId": "7", "projectId": "p1", "organizationId": "1", "roleKeys": ["viewer"]}`},
		{"update authorization", func(c *Client) error { return c.UpdateAuthorization(ctx, "a1", []string{"super-admin", "viewer"}) },
			authz + "UpdateAuthorization", `{"id": "a1", "roleKeys": ["super-admin", "viewer"]}`},
		{"delete authorization", func(c *Client) error { return c.DeleteAuthorization(ctx, "a1") },
			authz + "DeleteAuthorization", `{"id": "a1"}`},

		{"list projects with every filter", func(c *Client) error {
			_, err := c.ListProjects(ctx, ProjectFilter{Name: "plat", OrganizationID: "1"}, Page{Offset: 20, Limit: 20})
			return err
		}, project + "ListProjects", `{"pagination": {"offset": 20, "limit": 20, "asc": true}, "sortingColumn": "PROJECT_FIELD_NAME_NAME",
			"filters": [{"projectNameFilter": {"projectName": "plat", "method": "TEXT_FILTER_METHOD_CONTAINS_IGNORE_CASE"}},
			            {"organizationIdFilter": {"organizationId": "1", "type": "OWNED"}}]}`},
		{"get project", func(c *Client) error {
			_, err := c.GetProject(ctx, "p1")
			if HasCode(err, CodeNotFound) {
				err = nil
			}
			return err
		}, project + "GetProject", `{"projectId": "p1"}`},
		{"list project roles", func(c *Client) error { _, err := c.ListProjectRoles(ctx, "p1", Page{Limit: 100}); return err },
			project + "ListProjectRoles", `{"projectId": "p1", "pagination": {"limit": 100, "asc": true}, "sortingColumn": "PROJECT_ROLE_FIELD_NAME_KEY"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newFake(t, nil)
			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			got := f.last()
			if got.path != tc.path {
				t.Fatalf("path %s, want %s", got.path, tc.path)
			}
			if want := jsonValue(t, tc.body); !reflect.DeepEqual(got.body, want) {
				gotJSON, _ := json.Marshal(got.body)
				t.Fatalf("body\n%s\nwant\n%s", gotJSON, tc.body)
			}
		})
	}
}

// TestDecodesProtoJSON reads answers as protojson writes them: 64-bit
// integers as strings, enums by name, timestamps in RFC 3339 and fields
// with default values left out.
func TestDecodesProtoJSON(t *testing.T) {
	_, c := newFake(t, map[string]string{
		"/zitadel.org.v2.OrganizationService/ListOrganizations": `{
			"details": {"totalResult": "2", "timestamp": "2026-11-02T10:00:00Z"},
			"sortingColumn": "ORGANIZATION_FIELD_NAME_NAME",
			"result": [
				{"id": "1", "details": {"sequence": "12", "changeDate": "2026-11-01T09:30:00.123456Z", "resourceOwner": "1", "creationDate": "2026-10-01T08:00:00Z"},
				 "state": "ORGANIZATION_STATE_ACTIVE", "name": "tenant-a", "primaryDomain": "tenant-a.zitadel.localtest.me"},
				{"id": "2", "state": "ORGANIZATION_STATE_INACTIVE", "name": "tenant-b"}]}`,
		"/zitadel.user.v2.UserService/ListUsers": `{
			"details": {"totalResult": "2"},
			"result": [
				{"userId": "7", "details": {"resourceOwner": "1"}, "state": "USER_STATE_ACTIVE", "username": "ann",
				 "loginNames": ["ann@tenant-a.zitadel.localtest.me"], "preferredLoginName": "ann@tenant-a.zitadel.localtest.me",
				 "human": {"userId": "7", "profile": {"givenName": "Ann", "familyName": "Lee", "displayName": "Ann Lee"},
				           "email": {"email": "ann@a.test", "isVerified": true}, "passwordChanged": "2026-10-02T08:00:00Z"}},
				{"userId": "8", "details": {"resourceOwner": "1"}, "state": "USER_STATE_LOCKED", "username": "ci",
				 "machine": {"name": "CI", "hasSecret": true}}]}`,
		"/zitadel.authorization.v2.AuthorizationService/ListAuthorizations": `{
			"pagination": {"totalResult": "1", "appliedLimit": "100"},
			"authorizations": [{"id": "a1", "creationDate": "2026-10-03T08:00:00Z",
				"project": {"id": "p1", "name": "platform-admin", "organizationId": "0"},
				"organization": {"id": "0", "name": "ZITADEL"},
				"user": {"id": "7", "preferredLoginName": "ann@a", "displayName": "Ann Lee", "organizationId": "1"},
				"state": "STATE_ACTIVE", "roles": [{"key": "viewer", "displayName": "Viewer"}]}]}`,
		"/zitadel.project.v2.ProjectService/ListProjects": `{
			"pagination": {"totalResult": "1"},
			"projects": [{"projectId": "p1", "organizationId": "0", "name": "platform-admin", "state": "PROJECT_STATE_ACTIVE",
				"projectRoleAssertion": true, "projectAccessRequired": true}]}`,
	})
	ctx := context.Background()

	orgs, err := c.ListOrganizations(ctx, OrganizationFilter{}, Page{})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 1, 9, 30, 0, 123456000, time.UTC)
	if orgs.Total != 2 || len(orgs.Organizations) != 2 || orgs.Organizations[0].Details.Sequence != 12 ||
		!orgs.Organizations[0].Details.ChangeDate.Equal(want) || orgs.Organizations[1].State != OrganizationInactive ||
		orgs.Organizations[1].PrimaryDomain != "" {
		t.Fatalf("organizations = %+v", orgs)
	}

	users, err := c.ListUsers(ctx, UserFilter{}, Page{})
	if err != nil {
		t.Fatal(err)
	}
	ann, ci := users.Users[0], users.Users[1]
	if users.Total != 2 || ann.Human == nil || ann.Machine != nil || ann.Human.Profile.DisplayName != "Ann Lee" ||
		!ann.Human.Email.IsVerified || ann.Details.ResourceOwner != "1" || ann.Human.PasswordChanged.IsZero() {
		t.Fatalf("human = %+v", ann)
	}
	// accessTokenType is left out for the default, bearer.
	if ci.Machine == nil || ci.Human != nil || !ci.Machine.HasSecret || ci.Machine.AccessTokenType != "" || ci.State != UserLocked {
		t.Fatalf("machine = %+v", ci)
	}

	grants, err := c.ListAuthorizations(ctx, AuthorizationFilter{}, Page{})
	if err != nil {
		t.Fatal(err)
	}
	a := grants.Authorizations[0]
	if grants.Total != 1 || a.Project.Name != "platform-admin" || a.Organization.Name != "ZITADEL" || a.User.ID != "7" ||
		a.State != AuthorizationActive || len(a.Roles) != 1 || a.Roles[0].Key != "viewer" {
		t.Fatalf("authorizations = %+v", grants)
	}

	projects, err := c.ListProjects(ctx, ProjectFilter{}, Page{})
	if err != nil {
		t.Fatal(err)
	}
	p := projects.Projects[0]
	if projects.Total != 1 || p.ID != "p1" || !p.ProjectRoleAssertion || p.AuthorizationRequired || !p.ProjectAccessRequired {
		t.Fatalf("projects = %+v", projects)
	}
}

func TestCreateCallsReturnTheNewID(t *testing.T) {
	_, c := newFake(t, map[string]string{
		"/zitadel.org.v2.OrganizationService/AddOrganization":                `{"details": {}, "organizationId": "o9"}`,
		"/zitadel.user.v2.UserService/CreateUser":                            `{"id": "u9", "creationDate": "2026-11-02T10:00:00Z"}`,
		"/zitadel.authorization.v2.AuthorizationService/CreateAuthorization": `{"id": "a9"}`,
		"/zitadel.user.v2.UserService/AddSecret":                             `{"creationDate": "2026-11-02T10:00:00Z", "clientSecret": "s3cr3t"}`,
	})
	ctx := context.Background()
	org, err1 := c.AddOrganization(ctx, "x")
	user, err2 := c.CreateMachineUser(ctx, NewMachineUser{OrganizationID: org, Username: "m", Name: "M"})
	grant, err3 := c.CreateAuthorization(ctx, user, "p", org, []string{"r"})
	secret, err4 := c.AddSecret(ctx, user)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		t.Fatal(err)
	}
	if org != "o9" || user != "u9" || grant != "a9" || secret != "s3cr3t" {
		t.Fatalf("got %q %q %q %q", org, user, grant, secret)
	}
}

func TestErrorsCarryTheConnectCode(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		code, msg   string
		contentType string
	}{
		{"connect error", 404, `{"code": "not_found", "message": "User could not be found (QUERY-Dfbg2)"}`, CodeNotFound, "User could not be found (QUERY-Dfbg2)", "application/json"},
		{"connect validation error", 400, `{"code": "invalid_argument", "message": "invalid AddOrganizationRequest.Name"}`, CodeInvalidArgument, "invalid AddOrganizationRequest.Name", "application/json"},
		{"v1 gateway error", 409, `{"code": 6, "message": "User already exists", "details": []}`, CodeAlreadyExists, "User already exists", "application/json"},
		{"proxy page", 502, `<html>bad gateway</html>`, CodeUnavailable, "Bad Gateway", "text/html"},
		{"no route", 404, ``, CodeUnimplemented, "Not Found", "text/plain"},
		{"bad token", 401, `{"code": "unauthenticated", "message": "auth header missing"}`, CodeUnauthenticated, "auth header missing", "application/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			err := NewClient(srv.URL, staticTokens("t"), srv.Client()).DeleteUser(context.Background(), "7")
			var zerr *Error
			if !errors.As(err, &zerr) || zerr.Status != tc.status || zerr.Code != tc.code || zerr.Message != tc.msg {
				t.Fatalf("err = %#v, want status %d code %s message %q", err, tc.status, tc.code, tc.msg)
			}
			if !HasCode(err, tc.code) {
				t.Errorf("HasCode(%s) = false", tc.code)
			}
		})
	}
}

func TestGetByListReportsNotFound(t *testing.T) {
	_, c := newFake(t, map[string]string{
		"/zitadel.org.v2.OrganizationService/ListOrganizations":             `{"details": {}}`,
		"/zitadel.authorization.v2.AuthorizationService/ListAuthorizations": `{"pagination": {}}`,
	})
	if _, err := c.GetOrganization(context.Background(), "nope"); !HasCode(err, CodeNotFound) {
		t.Errorf("GetOrganization: %v, want not_found", err)
	}
	if _, err := c.GetAuthorization(context.Background(), "nope"); !HasCode(err, CodeNotFound) {
		t.Errorf("GetAuthorization: %v, want not_found", err)
	}
	if id, err := c.DefaultOrganizationID(context.Background()); err != nil || id != "" {
		t.Errorf("DefaultOrganizationID = %q, %v", id, err)
	}
}

func TestServiceAccountIsLookedUpOnce(t *testing.T) {
	calls, fail := 0, true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/auth/v1/users/me" || r.Header.Get("Authorization") != "Bearer t" {
			t.Errorf("%s %s with %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"user": {"id": "sa", "details": {"sequence": "3", "resourceOwner": "0"}, "userName": "admin-app"}}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, staticTokens("t"), srv.Client())

	if _, err := c.ServiceAccount(context.Background()); !HasCode(err, CodeUnavailable) {
		t.Fatalf("err = %v, want unavailable", err)
	}
	fail = false
	for range 2 {
		account, err := c.ServiceAccount(context.Background())
		if err != nil || account != (ServiceAccount{UserID: "sa", OrganizationID: "0"}) {
			t.Fatalf("account = %+v, %v", account, err)
		}
	}
	if calls != 2 {
		t.Fatalf("%d lookups, want 2 (one failed, then one kept)", calls)
	}
}

func TestUint64AcceptsStringsAndNumbers(t *testing.T) {
	var v struct{ A, B, C Uint64 }
	if err := json.Unmarshal([]byte(`{"A": "18446744073709551615", "B": 7, "C": null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != 18446744073709551615 || v.B != 7 || v.C != 0 {
		t.Fatalf("got %+v", v)
	}
	if err := json.Unmarshal([]byte(`{"A": "x"}`), &v); err == nil {
		t.Fatal("a non-number was accepted")
	}
}
