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
)

type call struct {
	Path  string
	OrgID string
	Body  map[string]any
}

// fakeZitadel records every call and answers from answers[path], 200 {} by default.
type fakeZitadel struct {
	t       *testing.T
	calls   []call
	answers map[string]func() (int, string)
}

func newFake(t *testing.T) (*fakeZitadel, *Client) {
	t.Helper()
	f := &fakeZitadel{t: t, answers: map[string]func() (int, string){}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, NewClient(srv.URL+"/", staticTokens("test-token"), srv.Client())
}

func (f *fakeZitadel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		f.t.Errorf("%s %s: method %s", r.Method, r.URL.Path, r.Method)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		f.t.Errorf("%s: Authorization = %q", r.URL.Path, got)
	}
	if got := r.Header.Get("Content-Type"); got != "application/json" {
		f.t.Errorf("%s: Content-Type = %q", r.URL.Path, got)
	}
	data, _ := io.ReadAll(r.Body)
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		f.t.Errorf("%s: body %q: %v", r.URL.Path, data, err)
	}
	f.calls = append(f.calls, call{Path: r.URL.Path, OrgID: r.Header.Get("x-zitadel-orgid"), Body: body})
	status, answer := http.StatusOK, "{}"
	if a, ok := f.answers[r.URL.Path]; ok {
		status, answer = a()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, answer)
}

func (f *fakeZitadel) paths() []string {
	var p []string
	for _, c := range f.calls {
		p = append(p, c.Path)
	}
	return p
}

func answer(status int, body string) func() (int, string) {
	return func() (int, string) { return status, body }
}

const (
	pathAddOrg     = "/zitadel.org.v2.OrganizationService/AddOrganization"
	pathListOrgs   = "/zitadel.org.v2.OrganizationService/ListOrganizations"
	pathDeactivate = "/zitadel.org.v2.OrganizationService/DeactivateOrganization"
	pathActivate   = "/zitadel.org.v2.OrganizationService/ActivateOrganization"
	pathDeleteOrg  = "/zitadel.org.v2.OrganizationService/DeleteOrganization"
	pathAddHuman   = "/zitadel.user.v2.UserService/AddHumanUser"
	pathInvite     = "/zitadel.user.v2.UserService/CreateInviteCode"
	pathMembers    = "/management/v1/orgs/me/members"
	tenantID       = "11111111-1111-4111-8111-111111111111"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Verifies: tenant-management AC-2; T-02
func TestRequestsMatchTheProtos(t *testing.T) {
	f, c := newFake(t)
	ctx := context.Background()
	if err := c.CreateOrganization(ctx, tenantID, "Acme"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateAdmin(ctx, tenantID, tenantID+"-admin", "boss@acme.test"); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func(context.Context, string) error{c.DeactivateOrganization, c.ActivateOrganization, c.DeleteOrganization} {
		if err := fn(ctx, tenantID); err != nil {
			t.Fatal(err)
		}
	}

	want := []call{
		{Path: pathAddOrg, Body: decode(t, `{"name":"Acme","organizationId":"`+tenantID+`"}`)},
		{Path: pathAddHuman, Body: decode(t, `{"userId":"`+tenantID+`-admin","username":"boss@acme.test",
			"organization":{"orgId":"`+tenantID+`"},"profile":{"givenName":"Tenant","familyName":"Admin"},
			"email":{"email":"boss@acme.test","returnCode":{}}}`)},
		{Path: pathMembers, OrgID: tenantID, Body: decode(t, `{"userId":"`+tenantID+`-admin","roles":["ORG_OWNER"]}`)},
		{Path: pathInvite, Body: decode(t, `{"userId":"`+tenantID+`-admin","sendCode":{"applicationName":"HAMSP"}}`)},
		{Path: pathDeactivate, Body: decode(t, `{"organizationId":"`+tenantID+`"}`)},
		{Path: pathActivate, Body: decode(t, `{"organizationId":"`+tenantID+`"}`)},
		{Path: pathDeleteOrg, Body: decode(t, `{"organizationId":"`+tenantID+`"}`)},
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls:\n got %+v\nwant %+v", f.calls, want)
	}
}

// Verifies: tenant-management AC-3; T-05
func TestCreateOrganizationIsIdempotent(t *testing.T) {
	ctx := context.Background()

	f, c := newFake(t)
	f.answers[pathAddOrg] = answer(http.StatusConflict, `{"code":"already_exists","message":"exists"}`)
	f.answers[pathListOrgs] = answer(http.StatusOK, `{"result":[{"id":"`+tenantID+`","state":"ORGANIZATION_STATE_ACTIVE"}]}`)
	if err := c.CreateOrganization(ctx, tenantID, "Acme"); err != nil {
		t.Fatalf("organization from an earlier attempt: %v", err)
	}
	if got := f.calls[1].Body; !reflect.DeepEqual(got, decode(t, `{"queries":[{"idQuery":{"id":"`+tenantID+`"}}]}`)) {
		t.Fatalf("list body = %v", got)
	}

	// The name belongs to another organization: not ours to adopt.
	f, c = newFake(t)
	f.answers[pathAddOrg] = answer(http.StatusConflict, `{"code":"already_exists","message":"exists"}`)
	f.answers[pathListOrgs] = answer(http.StatusOK, `{"result":[]}`)
	if err := c.CreateOrganization(ctx, tenantID, "Acme"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
}

// Verifies: tenant-management AC-3; T-05
func TestCreateAdminIsIdempotent(t *testing.T) {
	ctx := context.Background()
	f, c := newFake(t)
	f.answers[pathAddHuman] = answer(http.StatusConflict, `{"code":"already_exists","message":"user exists"}`)
	// The v1 gateway answers with a numeric gRPC code (6 = already exists).
	f.answers[pathMembers] = answer(http.StatusConflict, `{"code":6,"message":"member exists"}`)
	f.answers[pathInvite] = answer(http.StatusBadRequest, `{"code":"failed_precondition","message":"already initialised"}`)
	if err := c.CreateAdmin(ctx, tenantID, tenantID+"-admin", "boss@acme.test"); err != nil {
		t.Fatalf("admin from an earlier attempt: %v", err)
	}
	if got := f.paths(); !reflect.DeepEqual(got, []string{pathAddHuman, pathMembers, pathInvite}) {
		t.Fatalf("calls = %v", got)
	}

	// A user created just now that cannot be invited is an error.
	f, c = newFake(t)
	f.answers[pathInvite] = answer(http.StatusBadRequest, `{"code":"failed_precondition","message":"no email"}`)
	if err := c.CreateAdmin(ctx, tenantID, tenantID+"-admin", "boss@acme.test"); !HasCode(err, CodeFailedPrecondition) {
		t.Fatalf("err = %v", err)
	}
}

// Verifies: tenant-management AC-3, AC-6, AC-7; T-05
func TestStateChangesAreIdempotent(t *testing.T) {
	ctx := context.Background()
	f, c := newFake(t)
	f.answers[pathDeactivate] = answer(http.StatusBadRequest, `{"code":"failed_precondition","message":"not active"}`)
	f.answers[pathActivate] = answer(http.StatusBadRequest, `{"code":"failed_precondition","message":"not inactive"}`)
	f.answers[pathDeleteOrg] = answer(http.StatusNotFound, `{"code":"not_found","message":"no org"}`)
	for name, fn := range map[string]func(context.Context, string) error{
		"deactivate": c.DeactivateOrganization, "activate": c.ActivateOrganization, "delete": c.DeleteOrganization,
	} {
		if err := fn(ctx, tenantID); err != nil {
			t.Errorf("%s again: %v", name, err)
		}
	}
}

// Verifies: tenant-management AC-4; T-06
func TestErrorsCarryTheCode(t *testing.T) {
	ctx := context.Background()
	f, c := newFake(t)
	f.answers[pathAddOrg] = answer(http.StatusServiceUnavailable, `upstream down`)
	err := c.CreateOrganization(ctx, tenantID, "Acme")
	var zerr *Error
	if !errors.As(err, &zerr) || zerr.Status != http.StatusServiceUnavailable || zerr.Code != "unknown" {
		t.Fatalf("err = %#v", err)
	}
	f.answers[pathAddOrg] = answer(http.StatusGatewayTimeout, ``)
	if err := c.CreateOrganization(ctx, tenantID, "Acme"); !HasCode(err, CodeDeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.DeleteOrganization(canceled, tenantID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: err = %v", err)
	}
}
