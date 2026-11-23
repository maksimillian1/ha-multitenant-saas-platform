package e2e

// Boundary tests of a tenant admin (feature tenant-self-admin): the ORG_OWNER
// of tenant A's organization, as the Zitadel Console uses it. Zitadel checks
// permissions by role, not by user type, so the tests act as a machine user
// with ORG_OWNER in organization A, created and removed by iam-admin.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// zitadelOrgCall is zitadelCall in the context of organization orgID.
func zitadelOrgCall(t testing.TB, bearer, orgID, method, path string, body any) response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, cfg.Issuer+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-zitadel-orgid", orgID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return response{Status: resp.StatusCode, Body: raw}
}

// orgOwnerToken creates a machine user with ORG_OWNER in orgID and returns its
// access token for Zitadel's API; the user is deleted when the test ends.
func orgOwnerToken(t *testing.T, iamAdmin, orgID string) string {
	t.Helper()
	r := zitadelOrgCall(t, iamAdmin, orgID, http.MethodPost, "/management/v1/users/machine", map[string]any{
		"userName": "e2e-org-owner-" + randomID(), "name": "e2e org owner",
		"accessTokenType": "ACCESS_TOKEN_TYPE_JWT",
	})
	mustStatus(t, r, http.StatusOK, "create the org owner machine user")
	var user struct {
		UserID string `json:"userId"`
	}
	r.decode(t, &user)
	t.Cleanup(func() {
		d := zitadelOrgCall(t, iamAdmin, orgID, http.MethodDelete, "/management/v1/users/"+user.UserID, nil)
		if d.Status != http.StatusOK {
			t.Errorf("delete the org owner machine user: %d %s", d.Status, d.Body)
		}
	})
	mustStatus(t, zitadelOrgCall(t, iamAdmin, orgID, http.MethodPost, "/management/v1/orgs/me/members",
		map[string]any{"userId": user.UserID, "roles": []string{"ORG_OWNER"}}), http.StatusOK, "make the user ORG_OWNER")
	r = zitadelOrgCall(t, iamAdmin, orgID, http.MethodPost, "/management/v1/users/"+user.UserID+"/keys", map[string]any{
		"type": "KEY_TYPE_JSON", "expirationDate": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	mustStatus(t, r, http.StatusOK, "add a key to the org owner")
	var key struct {
		KeyDetails string `json:"keyDetails"`
	}
	r.decode(t, &key)
	raw, err := base64.StdEncoding.DecodeString(key.KeyDetails)
	if err != nil {
		t.Fatalf("key of the org owner: %v", err)
	}
	var k serviceKey
	if err := json.Unmarshal(raw, &k); err != nil {
		t.Fatalf("key of the org owner: %v", err)
	}
	return keyToken(t, k)
}

// crossOrgCall is one read or change of another organization's data.
type crossOrgCall struct {
	what   string
	method string
	path   string
	body   any
}

var crossOrgCalls = []crossOrgCall{
	{"list users", http.MethodPost, "/management/v1/users/_search", map[string]any{}},
	{"list role grants", http.MethodPost, "/management/v1/users/grants/_search", map[string]any{}},
	{"list identity providers", http.MethodPost, "/management/v1/idps/templates/_search", map[string]any{}},
	{"read the label policy", http.MethodGet, "/management/v1/policies/label", nil},
	{"create a user", http.MethodPost, "/management/v1/users/machine", map[string]any{
		"userName": "e2e-cross-org-" + randomID(), "name": "e2e cross org",
	}},
	{"change the label policy", http.MethodPut, "/management/v1/policies/label", map[string]any{"primaryColor": "#123456"}},
}

// Verifies: tenant-self-admin AC-3; T-01, T-12
func TestOrgOwnerCannotReachOtherOrg(t *testing.T) {
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	owner := orgOwnerToken(t, iamAdmin, tenA.OrgID)

	// Control: in its own organization the same reads pass, so a denial below
	// is the organization boundary and not a broken token.
	for _, c := range crossOrgCalls[:4] {
		mustStatus(t, zitadelOrgCall(t, owner, tenA.OrgID, c.method, c.path, c.body), http.StatusOK, c.what+" in its own org")
	}

	for _, c := range crossOrgCalls {
		r := zitadelOrgCall(t, owner, tenB.OrgID, c.method, c.path, c.body)
		if r.Status != http.StatusForbidden && r.Status != http.StatusNotFound {
			t.Errorf("%s of tenant B as tenant A's ORG_OWNER: status %d, want 403 or 404; body %s", c.what, r.Status, r.Body)
		}
	}

	r := zitadelCall(t, owner, http.MethodPost, "/zitadel.org.v2.OrganizationService/ListOrganizations", map[string]any{})
	mustStatus(t, r, http.StatusOK, "list organizations as tenant A's ORG_OWNER")
	var orgs struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	r.decode(t, &orgs)
	seenA := false
	for _, o := range orgs.Result {
		if o.ID == tenB.OrgID {
			t.Errorf("tenant A's ORG_OWNER sees organization B (%s) in the organization list", tenB.OrgID)
		}
		seenA = seenA || o.ID == tenA.OrgID
	}
	if !seenA {
		t.Errorf("tenant A's ORG_OWNER does not see its own organization in the list")
	}

	// Canary: iam-admin passes the reads into organization B, so the checks
	// above do detect cross-org access.
	for _, c := range crossOrgCalls[:4] {
		mustStatus(t, zitadelOrgCall(t, iamAdmin, tenB.OrgID, c.method, c.path, c.body), http.StatusOK, c.what+" of tenant B as iam-admin")
	}
}

// Verifies: tenant-self-admin AC-4; T-04
func TestOrgOwnerCannotChangeInstance(t *testing.T) {
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	owner := orgOwnerToken(t, iamAdmin, tenA.OrgID)

	denied := func(r response, what string) {
		t.Helper()
		if r.Status != http.StatusForbidden {
			t.Errorf("%s as ORG_OWNER: status %d, want 403; body %s", what, r.Status, r.Body)
		}
	}
	appKey := loadServiceKey(t, adminAppKeySecret, adminAppKeyField)
	// The target is already a member, so a call that passed the check would
	// fail with already_exists and change nothing.
	denied(zitadelCall(t, owner, http.MethodPost, "/admin/v1/members",
		map[string]any{"userId": appKey.UserID, "roles": []string{"IAM_OWNER"}}), "add an instance member")
	r := zitadelCall(t, owner, http.MethodPost, "/admin/v1/idps/generic_oidc", map[string]any{
		"name": "e2e-denied-" + randomID(), "issuer": "https://idp.example.invalid", "clientId": "e2e", "clientSecret": "e2e", // gitleaks:allow
		"scopes": []string{"openid"},
	})
	if r.Status == http.StatusOK {
		var created struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(r.Body, &created)
		zitadelCall(t, iamAdmin, http.MethodDelete, "/admin/v1/idps/templates/"+created.ID, nil)
	}
	denied(r, "add an instance identity provider")
	denied(zitadelCall(t, owner, http.MethodPut, "/admin/v1/policies/label", map[string]any{"primaryColor": "#123456"}),
		"change the instance label policy")
}

// consoleScope is what the test asks for on the Console's client: the user's
// organization in the token and Zitadel's API in its audience.
const consoleScope = "openid profile urn:zitadel:iam:user:resourceowner urn:zitadel:iam:org:project:id:zitadel:aud"

// humanOrgOwner invites a human user into orgID, makes it ORG_OWNER, and signs it
// in to the Console through Login V2.
func humanOrgOwner(t *testing.T, iamAdmin, orgID string) string {
	t.Helper()
	userID, loginName, password := invitedHuman(t, iamAdmin, iamAdmin, orgID)
	mustStatus(t, zitadelOrgCall(t, iamAdmin, orgID, http.MethodPost, "/management/v1/orgs/me/members",
		map[string]any{"userId": userID, "roles": []string{"ORG_OWNER"}}), http.StatusOK, "make the human user ORG_OWNER")
	return humanToken(t, iamAdmin, consoleClient(t), consoleScope, loginName, password)
}

// Verifies: tenant-self-admin AC-1
func TestOrgOwnerLogsInThroughLoginV2(t *testing.T) {
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	owner := humanOrgOwner(t, iamAdmin, tenA.OrgID)

	r := zitadelCall(t, owner, http.MethodPost, "/management/v1/orgs/me/members/_search", map[string]any{})
	mustStatus(t, r, http.StatusOK, "read the members of its organization with the Console token")
	_, _, _, claims := jwtParts(t, owner)
	if claims[tenantClaim] != tenA.OrgID {
		t.Errorf("Console token organization = %v, want tenant A %s", claims[tenantClaim], tenA.OrgID)
	}
}

// Verifies: tenant-self-admin AC-2
func TestOrgOwnerCreatesUserWithRole(t *testing.T) {
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	owner := humanOrgOwner(t, iamAdmin, tenA.OrgID)

	r := zitadelCall(t, owner, http.MethodPost, "/management/v1/projects", map[string]any{"name": "e2e-project-" + randomID()})
	mustStatus(t, r, http.StatusOK, "create a project as ORG_OWNER")
	var project struct {
		ID string `json:"id"`
	}
	r.decode(t, &project)
	t.Cleanup(func() {
		zitadelOrgCall(t, iamAdmin, tenA.OrgID, http.MethodDelete, "/management/v1/projects/"+project.ID, nil)
	})
	mustStatus(t, zitadelCall(t, owner, http.MethodPost, "/management/v1/projects/"+project.ID+"/roles",
		map[string]string{"roleKey": "viewer", "displayName": "Viewer"}), http.StatusOK, "add a project role as ORG_OWNER")

	userID, loginName, password := invitedHuman(t, iamAdmin, owner, tenA.OrgID)
	mustStatus(t, zitadelCall(t, owner, http.MethodPost, "/management/v1/users/"+userID+"/grants",
		map[string]any{"projectId": project.ID, "roleKeys": []string{"viewer"}}), http.StatusOK, "grant the role as ORG_OWNER")

	scope := "openid urn:zitadel:iam:user:resourceowner urn:zitadel:iam:org:project:id:" + project.ID + ":aud urn:zitadel:iam:org:project:role:viewer"
	tok := humanToken(t, iamAdmin, consoleClient(t), scope, loginName, password)
	_, _, _, claims := jwtParts(t, tok)
	if claims[tenantClaim] != tenA.OrgID {
		t.Errorf("new user's token organization = %v, want tenant A %s", claims[tenantClaim], tenA.OrgID)
	}
}
