package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

// fakeZitadel is an in-memory Zitadel for the handler tests. It records the
// name of every method called, so a test can check that nothing reached
// Zitadel, and the arguments of the last calls.
type fakeZitadel struct {
	mu    sync.Mutex
	calls []string
	err   error // every call fails with it when set

	account        zitadel.ServiceAccount
	defaultOrg     string
	orgs           map[string]*zitadel.Organization
	users          map[string]*zitadel.User
	authorizations map[string]*zitadel.Authorization
	projects       map[string]*zitadel.Project
	roles          map[string][]zitadel.ProjectRole
	secrets        map[string]string
	nextID         int

	orgFilter     zitadel.OrganizationFilter
	userFilter    zitadel.UserFilter
	projectFilter zitadel.ProjectFilter
	page          zitadel.Page
	newHuman      zitadel.NewHumanUser
	newMachine    zitadel.NewMachineUser
	update        zitadel.UserUpdate
	password      [2]string // user ID, password
	changeReq     bool
	newGrant      [3]string // user, project, organization
	roleKeys      []string
}

var created1 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

// newFakeZitadel holds the instance after bootstrap-tenants.sh: the default
// organization with the super admin, the service account and the platform
// projects, and two tenants.
func newFakeZitadel() *fakeZitadel {
	z := &fakeZitadel{
		account:        zitadel.ServiceAccount{UserID: "sa", OrganizationID: "org-zitadel"},
		defaultOrg:     "org-zitadel",
		orgs:           map[string]*zitadel.Organization{},
		users:          map[string]*zitadel.User{},
		authorizations: map[string]*zitadel.Authorization{},
		projects:       map[string]*zitadel.Project{},
		roles:          map[string][]zitadel.ProjectRole{},
		secrets:        map[string]string{},
	}
	for _, o := range []struct {
		id, name string
		state    zitadel.OrganizationState
	}{
		{"org-zitadel", "ZITADEL", zitadel.OrganizationActive},
		{"org-a", "tenant-a", zitadel.OrganizationActive},
		{"org-b", "tenant-b", zitadel.OrganizationInactive},
	} {
		z.orgs[o.id] = &zitadel.Organization{ID: o.id, Name: o.name, State: o.state, PrimaryDomain: o.name + ".localtest.me",
			Details: zitadel.Details{CreationDate: created1, ResourceOwner: o.id}}
	}
	z.addHuman("admin", "org-zitadel", "super-admin", "Super", "Admin")
	z.addHuman("ann", "org-a", "ann", "Ann", "Lee")
	z.addMachine("sa", "org-zitadel", "admin-app", "Admin app")
	z.addMachine("ci", "org-a", "tenant-a-client", "tenant-a API client")
	z.users["ci"].Machine.HasSecret = true
	z.users["ci"].Machine.AccessTokenType = zitadel.AccessTokenJWT

	z.projects["p-admin"] = &zitadel.Project{ID: "p-admin", OrganizationID: "org-zitadel", Name: "platform-admin",
		State: zitadel.ProjectActive, ProjectRoleAssertion: true, AuthorizationRequired: true, CreationDate: created1}
	z.projects["p-api"] = &zitadel.Project{ID: "p-api", OrganizationID: "org-zitadel", Name: "platform-api", State: zitadel.ProjectActive}
	z.roles["p-admin"] = []zitadel.ProjectRole{{Key: "super-admin", DisplayName: "super-admin"}, {Key: "viewer", DisplayName: "viewer"}}
	z.roles["p-api"] = []zitadel.ProjectRole{{Key: "reader"}}
	z.addGrant("grant-admin", "admin", "p-admin", "super-admin")
	z.addGrant("grant-ann", "ann", "p-admin", "viewer")
	return z
}

func (z *fakeZitadel) addHuman(id, org, username, given, family string) {
	u := &zitadel.User{UserID: id, State: zitadel.UserActive, Username: username,
		LoginNames: []string{username + "@" + z.orgs[org].PrimaryDomain}, PreferredLoginName: username + "@" + z.orgs[org].PrimaryDomain,
		Details: zitadel.Details{ResourceOwner: org, CreationDate: created1}, Human: &zitadel.HumanUser{}}
	u.Human.Profile.GivenName, u.Human.Profile.FamilyName = given, family
	u.Human.Email.Email, u.Human.Email.IsVerified = username+"@example.test", true
	z.users[id] = u
}

func (z *fakeZitadel) addMachine(id, org, username, name string) {
	z.users[id] = &zitadel.User{UserID: id, State: zitadel.UserActive, Username: username,
		Details: zitadel.Details{ResourceOwner: org}, Machine: &zitadel.MachineUser{Name: name}}
}

func (z *fakeZitadel) addGrant(id, userID, projectID string, roles ...string) {
	u, p := z.users[userID], z.projects[projectID]
	a := &zitadel.Authorization{ID: id, State: zitadel.AuthorizationActive, CreationDate: created1}
	a.Project.ID, a.Project.Name, a.Project.OrganizationID = p.ID, p.Name, p.OrganizationID
	a.Organization.ID, a.Organization.Name = p.OrganizationID, z.orgs[p.OrganizationID].Name
	a.User.ID, a.User.PreferredLoginName, a.User.OrganizationID = u.UserID, u.PreferredLoginName, u.Details.ResourceOwner
	for _, key := range roles {
		a.Roles = append(a.Roles, zitadel.Role{Key: key, DisplayName: key})
	}
	z.authorizations[id] = a
}

// record notes a call and returns the configured error.
func (z *fakeZitadel) record(method string) error {
	z.calls = append(z.calls, method)
	return z.err
}

func (z *fakeZitadel) called() []string {
	z.mu.Lock()
	defer z.mu.Unlock()
	return slices.Clone(z.calls)
}

func (z *fakeZitadel) id(prefix string) string {
	z.nextID++
	return prefix + "-" + strconv.Itoa(z.nextID)
}

func notFound(what string) error {
	return &zitadel.Error{Status: http.StatusNotFound, Code: zitadel.CodeNotFound, Message: what + " not found"}
}

func pageOf[T any](items []T, p zitadel.Page) []T {
	start := min(p.Offset, len(items))
	end := len(items)
	if p.Limit > 0 {
		end = min(start+p.Limit, len(items))
	}
	return items[start:end]
}

func contains(s, part string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(part))
}

func (z *fakeZitadel) ServiceAccount(context.Context) (zitadel.ServiceAccount, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.account, z.record("ServiceAccount")
}

func (z *fakeZitadel) ListOrganizations(_ context.Context, f zitadel.OrganizationFilter, p zitadel.Page) (zitadel.OrganizationList, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("ListOrganizations"); err != nil {
		return zitadel.OrganizationList{}, err
	}
	if !f.Default && f.ID == "" {
		z.orgFilter, z.page = f, p
	}
	var match []zitadel.Organization
	for _, o := range z.orgs {
		if (f.Name == "" || contains(o.Name, f.Name)) && (f.State == "" || o.State == f.State) &&
			(f.ID == "" || o.ID == f.ID) && (!f.Default || o.ID == z.defaultOrg) {
			match = append(match, *o)
		}
	}
	slices.SortFunc(match, func(a, b zitadel.Organization) int { return strings.Compare(a.Name, b.Name) })
	return zitadel.OrganizationList{Total: uint64(len(match)), Organizations: pageOf(match, p)}, nil
}

func (z *fakeZitadel) GetOrganization(_ context.Context, id string) (zitadel.Organization, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("GetOrganization"); err != nil {
		return zitadel.Organization{}, err
	}
	if o, ok := z.orgs[id]; ok {
		return *o, nil
	}
	return zitadel.Organization{}, notFound("organization")
}

func (z *fakeZitadel) DefaultOrganizationID(context.Context) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.defaultOrg, z.record("DefaultOrganizationID")
}

func (z *fakeZitadel) AddOrganization(_ context.Context, name string) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("AddOrganization"); err != nil {
		return "", err
	}
	id := z.id("org")
	z.orgs[id] = &zitadel.Organization{ID: id, Name: name, State: zitadel.OrganizationActive}
	return id, nil
}

func (z *fakeZitadel) changeOrg(method, id string, change func(*zitadel.Organization)) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record(method); err != nil {
		return err
	}
	o, ok := z.orgs[id]
	if !ok {
		return notFound("organization")
	}
	change(o)
	return nil
}

func (z *fakeZitadel) UpdateOrganization(_ context.Context, id, name string) error {
	return z.changeOrg("UpdateOrganization", id, func(o *zitadel.Organization) { o.Name = name })
}

func (z *fakeZitadel) DeactivateOrganization(_ context.Context, id string) error {
	return z.changeOrg("DeactivateOrganization", id, func(o *zitadel.Organization) { o.State = zitadel.OrganizationInactive })
}

func (z *fakeZitadel) ActivateOrganization(_ context.Context, id string) error {
	return z.changeOrg("ActivateOrganization", id, func(o *zitadel.Organization) { o.State = zitadel.OrganizationActive })
}

func (z *fakeZitadel) DeleteOrganization(_ context.Context, id string) error {
	return z.changeOrg("DeleteOrganization", id, func(o *zitadel.Organization) { delete(z.orgs, o.ID) })
}

func (z *fakeZitadel) ListUsers(_ context.Context, f zitadel.UserFilter, p zitadel.Page) (zitadel.UserList, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("ListUsers"); err != nil {
		return zitadel.UserList{}, err
	}
	z.userFilter, z.page = f, p
	var match []zitadel.User
	for _, u := range z.users {
		search := u.Username
		if u.Human != nil {
			search += " " + u.Human.Profile.DisplayName + " " + u.Human.Email.Email
		}
		isMachine := u.Machine != nil
		if (f.Search == "" || contains(search, f.Search)) && (f.OrganizationID == "" || u.Details.ResourceOwner == f.OrganizationID) &&
			(f.Type == "" || (f.Type == zitadel.TypeMachine) == isMachine) && (f.State == "" || u.State == f.State) {
			match = append(match, *u)
		}
	}
	slices.SortFunc(match, func(a, b zitadel.User) int { return strings.Compare(a.Username, b.Username) })
	return zitadel.UserList{Total: uint64(len(match)), Users: pageOf(match, p)}, nil
}

func (z *fakeZitadel) GetUser(_ context.Context, id string) (zitadel.User, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("GetUser"); err != nil {
		return zitadel.User{}, err
	}
	if u, ok := z.users[id]; ok {
		return *u, nil
	}
	return zitadel.User{}, notFound("user")
}

func (z *fakeZitadel) CreateHumanUser(_ context.Context, u zitadel.NewHumanUser) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("CreateHumanUser"); err != nil {
		return "", err
	}
	if _, ok := z.orgs[u.OrganizationID]; !ok {
		return "", notFound("organization")
	}
	z.newHuman = u
	return z.id("user"), nil
}

func (z *fakeZitadel) CreateMachineUser(_ context.Context, u zitadel.NewMachineUser) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("CreateMachineUser"); err != nil {
		return "", err
	}
	z.newMachine = u
	return z.id("user"), nil
}

func (z *fakeZitadel) changeUser(method, id string, change func(*zitadel.User)) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record(method); err != nil {
		return err
	}
	u, ok := z.users[id]
	if !ok {
		return notFound("user")
	}
	change(u)
	return nil
}

func (z *fakeZitadel) UpdateUser(_ context.Context, id string, update zitadel.UserUpdate) error {
	return z.changeUser("UpdateUser", id, func(*zitadel.User) { z.update = update })
}

func (z *fakeZitadel) SetPassword(_ context.Context, id, password string, changeRequired bool) error {
	return z.changeUser("SetPassword", id, func(*zitadel.User) { z.password, z.changeReq = [2]string{id, password}, changeRequired })
}

func (z *fakeZitadel) DeactivateUser(_ context.Context, id string) error {
	return z.changeUser("DeactivateUser", id, func(u *zitadel.User) { u.State = zitadel.UserInactive })
}

func (z *fakeZitadel) ReactivateUser(_ context.Context, id string) error {
	return z.changeUser("ReactivateUser", id, func(u *zitadel.User) { u.State = zitadel.UserActive })
}

func (z *fakeZitadel) LockUser(_ context.Context, id string) error {
	return z.changeUser("LockUser", id, func(u *zitadel.User) { u.State = zitadel.UserLocked })
}

func (z *fakeZitadel) UnlockUser(_ context.Context, id string) error {
	return z.changeUser("UnlockUser", id, func(u *zitadel.User) { u.State = zitadel.UserActive })
}

func (z *fakeZitadel) DeleteUser(_ context.Context, id string) error {
	return z.changeUser("DeleteUser", id, func(u *zitadel.User) { delete(z.users, u.UserID) })
}

func (z *fakeZitadel) AddSecret(_ context.Context, id string) (string, error) {
	var secret string
	err := z.changeUser("AddSecret", id, func(u *zitadel.User) {
		secret = "secret-of-" + id
		z.secrets[id] = secret
		u.Machine.HasSecret = true
	})
	return secret, err
}

func (z *fakeZitadel) RemoveSecret(_ context.Context, id string) error {
	return z.changeUser("RemoveSecret", id, func(u *zitadel.User) { delete(z.secrets, id); u.Machine.HasSecret = false })
}

func (z *fakeZitadel) ListAuthorizations(_ context.Context, f zitadel.AuthorizationFilter, p zitadel.Page) (zitadel.AuthorizationList, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("ListAuthorizations"); err != nil {
		return zitadel.AuthorizationList{}, err
	}
	var match []zitadel.Authorization
	for _, a := range z.authorizations {
		if (len(f.IDs) == 0 || slices.Contains(f.IDs, a.ID)) && (f.UserID == "" || a.User.ID == f.UserID) &&
			(f.ProjectID == "" || a.Project.ID == f.ProjectID) {
			match = append(match, *a)
		}
	}
	slices.SortFunc(match, func(a, b zitadel.Authorization) int { return strings.Compare(a.ID, b.ID) })
	return zitadel.AuthorizationList{Total: uint64(len(match)), Authorizations: pageOf(match, p)}, nil
}

func (z *fakeZitadel) GetAuthorization(_ context.Context, id string) (zitadel.Authorization, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("GetAuthorization"); err != nil {
		return zitadel.Authorization{}, err
	}
	if a, ok := z.authorizations[id]; ok {
		return *a, nil
	}
	return zitadel.Authorization{}, notFound("role assignment")
}

func (z *fakeZitadel) CreateAuthorization(_ context.Context, userID, projectID, orgID string, roleKeys []string) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("CreateAuthorization"); err != nil {
		return "", err
	}
	z.newGrant, z.roleKeys = [3]string{userID, projectID, orgID}, roleKeys
	return z.id("grant"), nil
}

func (z *fakeZitadel) UpdateAuthorization(_ context.Context, id string, roleKeys []string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("UpdateAuthorization"); err != nil {
		return err
	}
	z.roleKeys = roleKeys
	return nil
}

func (z *fakeZitadel) DeleteAuthorization(_ context.Context, id string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("DeleteAuthorization"); err != nil {
		return err
	}
	delete(z.authorizations, id)
	return nil
}

func (z *fakeZitadel) ListProjects(_ context.Context, f zitadel.ProjectFilter, p zitadel.Page) (zitadel.ProjectList, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("ListProjects"); err != nil {
		return zitadel.ProjectList{}, err
	}
	z.projectFilter, z.page = f, p
	var match []zitadel.Project
	for _, project := range z.projects {
		if (f.Name == "" || contains(project.Name, f.Name)) && (f.OrganizationID == "" || project.OrganizationID == f.OrganizationID) {
			match = append(match, *project)
		}
	}
	slices.SortFunc(match, func(a, b zitadel.Project) int { return strings.Compare(a.Name, b.Name) })
	return zitadel.ProjectList{Total: uint64(len(match)), Projects: pageOf(match, p)}, nil
}

func (z *fakeZitadel) GetProject(_ context.Context, id string) (zitadel.Project, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("GetProject"); err != nil {
		return zitadel.Project{}, err
	}
	if project, ok := z.projects[id]; ok {
		return *project, nil
	}
	return zitadel.Project{}, notFound("project")
}

func (z *fakeZitadel) ListProjectRoles(_ context.Context, projectID string, _ zitadel.Page) ([]zitadel.ProjectRole, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.record("ListProjectRoles"); err != nil {
		return nil, err
	}
	return z.roles[projectID], nil
}

// env is the API with the fake Zitadel, signed in as the super admin
// "admin" of the default organization.
type env struct {
	t    *testing.T
	z    *fakeZitadel
	mux  *http.ServeMux
	logs *bytes.Buffer
}

const testOrigin = "http://admin.test"

func adminSession() fakeSessions {
	return fakeSessions{ok: true, session: core.Session{Subject: "admin", Name: "Super Admin", Roles: []string{"super-admin"},
		Expires: time.Now().Add(time.Hour)}}
}

func newEnvWith(t *testing.T, sessions fakeSessions) *env {
	t.Helper()
	logs := &bytes.Buffer{}
	z := newFakeZitadel()
	svc := NewService(sessions, z, "super-admin", testOrigin+"/", slog.New(slog.NewJSONHandler(logs, nil)))
	mux := http.NewServeMux()
	svc.Register(mux)
	return &env{t: t, z: z, mux: mux, logs: logs}
}

func newEnv(t *testing.T) *env { return newEnvWith(t, adminSession()) }

// do sends a request as the SPA does: same origin, with X-Requested-With.
// headers can drop a header by setting it to "".
func (e *env) do(method, target string, body any, headers ...[2]string) *httptest.ResponseRecorder {
	e.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, target, reader)
	r.Host = "admin.test"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, h := range headers {
		if h[1] == "" {
			r.Header.Del(h[0])
		} else {
			r.Header.Set(h[0], h[1])
		}
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, r)
	return rec
}

// expect checks the status and decodes the body into out, if given.
func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, out any) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("body %s: %v", rec.Body, err)
		}
	}
}

func errorText(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body Error
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == "" {
		t.Fatalf("body %s is not an error", rec.Body)
	}
	return body.Error
}
