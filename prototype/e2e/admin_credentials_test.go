package e2e

// Boundary tests of the Zitadel service accounts (feature admin-zitadel,
// HAMSP-36). They call Zitadel directly with short-lived tokens that they get
// from the accounts' JWT profile keys, read from the cluster's Secrets:
//
//   - iam-admin: the IAM_OWNER machine user of the Zitadel Helm chart, key in
//     the Secret zitadel/iam-admin (field iam-admin.json);
//   - admin-app: the admin app's service account, key in the Secret
//     admin/admin-zitadel (field key.json).
//
// The tests never print a key or a token.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	adminAppKeySecret  = "admin/admin-zitadel"
	adminAppKeyField   = "key.json"
	iamAdminKeySecret  = "zitadel/iam-admin"
	iamAdminKeyField   = "iam-admin.json"
	instanceSAUserName = "admin-app-instance"
	// zitadelAPIScope puts Zitadel's own API in the token's audience.
	zitadelAPIScope = "openid urn:zitadel:iam:org:project:id:zitadel:aud"
)

// serviceKey is a JWT profile key file as Zitadel issues it.
type serviceKey struct {
	Type   string `json:"type"`
	KeyID  string `json:"keyId"`
	Key    string `json:"key"`
	UserID string `json:"userId"`
}

// secretField reads one field of a Secret ("namespace/name"). It returns
// errNotFound when the Secret does not exist.
func secretField(ctx context.Context, secret, field string) (string, error) {
	ns, name, _ := strings.Cut(secret, "/")
	out, err := kubectl(ctx, "-n", ns, "get", "secret", name, "-o", "jsonpath={.data}")
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") {
			return "", errNotFound
		}
		return "", err
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		return "", fmt.Errorf("secret %s: %w", secret, err)
	}
	enc, ok := data[field]
	if !ok {
		return "", fmt.Errorf("secret %s has no field %s", secret, field)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("secret %s field %s: %w", secret, field, err)
	}
	return string(raw), nil
}

var errNotFound = errors.New("not found")

// loadServiceKey reads a JWT profile key from a Secret.
func loadServiceKey(t testing.TB, secret, field string) serviceKey {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	raw, err := secretField(ctx, secret, field)
	if err != nil {
		t.Fatalf("key of %s: %v", secret, err)
	}
	var k serviceKey
	if err := json.Unmarshal([]byte(raw), &k); err != nil || k.UserID == "" || k.KeyID == "" || k.Key == "" {
		t.Fatalf("secret %s field %s is not a JWT profile key file (decode error: %v)", secret, field, err)
	}
	return k
}

// keyToken exchanges a signed assertion for an access token (RFC 7523).
func keyToken(t testing.TB, k serviceKey) string {
	t.Helper()
	block, _ := pem.Decode([]byte(k.Key))
	if block == nil {
		t.Fatalf("key %s of user %s is not PEM", k.KeyID, k.UserID)
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("key %s of user %s: %v", k.KeyID, k.UserID, err)
	}
	enc := func(v any) string {
		raw, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	now := time.Now()
	input := enc(map[string]string{"alg": "RS256", "typ": "JWT", "kid": k.KeyID}) + "." + enc(map[string]any{
		"iss": k.UserID, "sub": k.UserID, "aud": cfg.Issuer,
		"iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
	})
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"scope":      {zitadelAPIScope},
		"assertion":  {input + "." + base64.RawURLEncoding.EncodeToString(sig)},
	}
	req, _ := http.NewRequest(http.MethodPost, cfg.Issuer+"/oauth/v2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("token for user %s: %v", k.UserID, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token for user %s: %d %s", k.UserID, resp.StatusCode, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		t.Fatalf("token for user %s: no access_token in the answer", k.UserID)
	}
	return tok.AccessToken
}

// zitadelCall sends one request to Zitadel's API at the issuer.
func zitadelCall(t testing.TB, bearer, method, path string, body any) response {
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.HasPrefix(path, "/zitadel.") {
		req.Header.Set("Connect-Protocol-Version", "1")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return response{Status: resp.StatusCode, Body: raw}
}

// instanceRoles returns the instance member roles of a user, read as iam-admin.
func instanceRoles(t testing.TB, iamAdmin, userID string) []string {
	t.Helper()
	r := zitadelCall(t, iamAdmin, http.MethodPost, "/admin/v1/members/_search",
		map[string]any{"queries": []any{map[string]any{"userIdQuery": map[string]string{"userId": userID}}}})
	mustStatus(t, r, http.StatusOK, "search instance members")
	var out struct {
		Result []struct {
			Roles []string `json:"roles"`
		} `json:"result"`
	}
	r.decode(t, &out)
	if len(out.Result) == 0 {
		return nil
	}
	roles := slices.Clone(out.Result[0].Roles)
	slices.Sort(roles)
	return roles
}

// mustBeDenied checks that Zitadel refused a call for lack of permission.
// When the call went through, cleanup undoes it before the test fails.
func mustBeDenied(t *testing.T, r response, what string, cleanup func(response)) {
	t.Helper()
	if r.Status == http.StatusForbidden {
		return
	}
	if r.Status == http.StatusOK && cleanup != nil {
		cleanup(r)
	}
	t.Errorf("%s as admin-app: status %d, want 403 permission_denied; body %s", what, r.Status, r.Body)
}

// Verifies: admin-zitadel AC-7; T-04, T-12
func TestAdminAppRoleBoundary(t *testing.T) {
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	appKey := loadServiceKey(t, adminAppKeySecret, adminAppKeyField)
	app := keyToken(t, appKey)

	if got, want := instanceRoles(t, iamAdmin, appKey.UserID), []string{"IAM_BRANDING_MANAGER", "IAM_ORG_MANAGER"}; !slices.Equal(got, want) {
		t.Errorf("instance roles of admin-app = %v, want %v", got, want)
	}

	// Adding an instance member: the target is admin-app itself, which is a
	// member already, so a call that passes the permission check fails with
	// already_exists and changes nothing.
	mustBeDenied(t, zitadelCall(t, app, http.MethodPost, "/admin/v1/members",
		map[string]any{"userId": appKey.UserID, "roles": []string{"IAM_OWNER"}}), "add an instance member", nil)

	name := "e2e-denied-" + randomID()
	mustBeDenied(t, zitadelCall(t, app, http.MethodPost, "/admin/v1/idps/generic_oidc", map[string]any{
		"name": name, "issuer": "https://idp.example.invalid", "clientId": "e2e", "clientSecret": "e2e", // gitleaks:allow
		"scopes": []string{"openid"},
	}), "add an instance identity provider", func(r response) {
		var created struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(r.Body, &created)
		zitadelCall(t, iamAdmin, http.MethodDelete, "/admin/v1/idps/"+created.ID, nil)
	})

	mustBeDenied(t, zitadelCall(t, app, http.MethodPost, "/zitadel.action.v2.ActionService/CreateTarget", map[string]any{
		"name": name, "endpoint": "https://action.example.invalid", "timeout": "5s",
		"restWebhook": map[string]any{},
	}), "add an instance action target", func(r response) {
		var created struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(r.Body, &created)
		zitadelCall(t, iamAdmin, http.MethodPost, "/zitadel.action.v2.ActionService/DeleteTarget", map[string]string{"id": created.ID})
	})

	// The instance branding is what IAM_BRANDING_MANAGER is for: change the
	// primary color of the preview and put it back.
	r := zitadelCall(t, app, http.MethodGet, "/admin/v1/policies/label/_preview", nil)
	mustStatus(t, r, http.StatusOK, "read the instance branding as admin-app")
	var current struct {
		Policy map[string]any `json:"policy"`
	}
	r.decode(t, &current)
	update := map[string]any{}
	for _, f := range []string{"primaryColor", "hideLoginNameSuffix", "warnColor", "backgroundColor", "fontColor",
		"primaryColorDark", "backgroundColorDark", "warnColorDark", "fontColorDark", "disableWatermark", "themeMode"} {
		if v, ok := current.Policy[f]; ok {
			update[f] = v
		}
	}
	original := update["primaryColor"]
	update["primaryColor"] = "#123456"
	if original == "#123456" {
		update["primaryColor"] = "#654321"
	}
	mustStatus(t, zitadelCall(t, app, http.MethodPut, "/admin/v1/policies/label", update), http.StatusOK,
		"change the instance branding as admin-app")
	update["primaryColor"] = original
	if original == nil {
		update["primaryColor"] = ""
	}
	mustStatus(t, zitadelCall(t, app, http.MethodPut, "/admin/v1/policies/label", update), http.StatusOK,
		"restore the instance branding as admin-app")
}

// Verifies: admin-zitadel AC-10; T-12
func TestIAMAdminHasNoPAT(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := secretField(ctx, "zitadel/iam-admin-pat", "pat"); !errors.Is(err, errNotFound) {
		t.Errorf("Secret zitadel/iam-admin-pat: %v, want it not to exist", err)
	}

	k := loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField)
	iamAdmin := keyToken(t, k)
	r := zitadelCall(t, iamAdmin, http.MethodPost, "/management/v1/users/"+k.UserID+"/pats/_search", map[string]any{})
	mustStatus(t, r, http.StatusOK, "list iam-admin's personal access tokens")
	var out struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	r.decode(t, &out)
	if len(out.Result) != 0 {
		ids := make([]string, 0, len(out.Result))
		for _, p := range out.Result {
			ids = append(ids, p.ID)
		}
		t.Errorf("iam-admin has personal access tokens %v, want none", ids)
	}
}

// Verifies: admin-zitadel AC-14
func TestNoInstanceServiceAccount(t *testing.T) {
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	r := zitadelCall(t, iamAdmin, http.MethodPost, "/zitadel.user.v2.UserService/ListUsers", map[string]any{
		"queries": []any{map[string]any{"userNameQuery": map[string]string{
			"userName": instanceSAUserName, "method": "TEXT_QUERY_METHOD_EQUALS",
		}}},
	})
	mustStatus(t, r, http.StatusOK, "search users")
	var out struct {
		Result []struct {
			UserID string `json:"userId"`
		} `json:"result"`
	}
	r.decode(t, &out)
	if len(out.Result) != 0 {
		t.Errorf("machine user %s exists (%s), want it deleted", instanceSAUserName, out.Result[0].UserID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, field := range []string{"ZITADEL_INSTANCE_TOKEN", "ZITADEL_TOKEN"} {
		if _, err := secretField(ctx, adminAppKeySecret, field); err == nil {
			t.Errorf("Secret %s still has %s, want only the key", adminAppKeySecret, field)
		}
	}
}

// machineKey is a key of a machine user as the management API lists it.
type machineKey struct {
	ID             string    `json:"id"`
	ExpirationDate time.Time `json:"expirationDate"`
}

// credentials lists the keys and personal access tokens of a machine user of
// the IAM admin's organization.
func credentials(t testing.TB, iamAdmin, userID string) (keys []machineKey, pats []string) {
	t.Helper()
	r := zitadelCall(t, iamAdmin, http.MethodPost, "/management/v1/users/"+userID+"/keys/_search", map[string]any{})
	mustStatus(t, r, http.StatusOK, "list keys of "+userID)
	var k struct {
		Result []machineKey `json:"result"`
	}
	r.decode(t, &k)
	r = zitadelCall(t, iamAdmin, http.MethodPost, "/management/v1/users/"+userID+"/pats/_search", map[string]any{})
	mustStatus(t, r, http.StatusOK, "list personal access tokens of "+userID)
	var p struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	r.decode(t, &p)
	for _, x := range p.Result {
		pats = append(pats, x.ID)
	}
	return k.Result, pats
}

// checkOnlyShortKey checks that a machine user has exactly the key keyID,
// valid at most 30 days, and no personal access token.
func checkOnlyShortKey(t *testing.T, iamAdmin, name, userID, keyID string) {
	t.Helper()
	keys, pats := credentials(t, iamAdmin, userID)
	if len(keys) != 1 || keys[0].ID != keyID {
		t.Errorf("%s has keys %+v, want only %s", name, keys, keyID)
	}
	for _, k := range keys {
		if limit := time.Now().Add(30*24*time.Hour + time.Hour); k.ExpirationDate.IsZero() || k.ExpirationDate.After(limit) {
			t.Errorf("key %s of %s expires %s, want at most 30 days from now", k.ID, name, k.ExpirationDate)
		}
	}
	if len(pats) != 0 {
		t.Errorf("%s has personal access tokens %v, want none", name, pats)
	}
}

// Verifies: admin-zitadel AC-5
func TestAdminAppHasOnlyShortKey(t *testing.T) {
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	k := loadServiceKey(t, adminAppKeySecret, adminAppKeyField)
	checkOnlyShortKey(t, iamAdmin, "admin-app", k.UserID, k.KeyID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := kubectl(ctx, "-n", "admin", "get", "secret", "admin-zitadel", "-o", "jsonpath={.data}")
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatal(err)
	}
	if _, ok := data[adminAppKeyField]; len(data) != 1 || !ok {
		fields := make([]string, 0, len(data))
		for f := range data {
			fields = append(fields, f)
		}
		t.Errorf("Secret %s has fields %v, want only %s", adminAppKeySecret, fields, adminAppKeyField)
	}
}

// Verifies: admin-zitadel AC-11
func TestBootstrapOutputWithoutPAT(t *testing.T) {
	// What bootstrap-tenants.sh created works: TestMain got a token for a
	// tenant and passed the gateway with it. Here: its output has the admin
	// app's users, and nothing it could have used is a token.
	var out struct {
		Admin struct {
			Users []struct {
				Username string `json:"username"`
			} `json:"users"`
		} `json:"admin"`
	}
	path := os.Getenv("HAMSP_TENANTS")
	if path == "" {
		path = "../.tenants.json"
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the bootstrap output the tests read anyway.
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Admin.Users) != 3 || len(cfg.Tenants) != 2 {
		t.Errorf("output has %d tenants and %d admin users, want 2 and 3", len(cfg.Tenants), len(out.Admin.Users))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := secretField(ctx, "zitadel/iam-admin-pat", "pat"); !errors.Is(err, errNotFound) {
		t.Errorf("Secret zitadel/iam-admin-pat: %v, want it not to exist", err)
	}
	k := loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField)
	if _, pats := credentials(t, keyToken(t, k), k.UserID); len(pats) != 0 {
		t.Errorf("iam-admin has personal access tokens %v, want none", pats)
	}
}

// Verifies: admin-zitadel AC-12, AC-5
func TestBootstrapRerunRotatesKeys(t *testing.T) {
	before := map[string]serviceKey{
		"iam-admin": loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField),
		"admin-app": loadServiceKey(t, adminAppKeySecret, adminAppKeyField),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "../bootstrap-tenants.sh")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("bootstrap-tenants.sh: %v\n%s", err, output.String())
	}
	// The run made new client secrets; read them as the next tests need them.
	t.Setenv("HAMSP_TENANTS", "../.tenants.json")
	if err := loadConfig(); err != nil {
		t.Fatal(err)
	}

	after := map[string]serviceKey{
		"iam-admin": loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField),
		"admin-app": loadServiceKey(t, adminAppKeySecret, adminAppKeyField),
	}
	iamAdmin := keyToken(t, after["iam-admin"])
	for name, k := range after {
		if k.KeyID == before[name].KeyID {
			t.Errorf("%s still has the key %s of before the run", name, k.KeyID)
		}
		checkOnlyShortKey(t, iamAdmin, name, k.UserID, k.KeyID)
	}
}

// driftEvent is the event the admin app logs for a credential a service
// account should not have (apps/admin/zitadel.DriftEvent).
const driftEvent = "service_account_credential_drift"

// Verifies: admin-zitadel AC-9
func TestCredentialDriftLogged(t *testing.T) {
	// A bootstrap run just before (TestBootstrapRerunRotatesKeys) restarts
	// the admin app; its new pod must be up to log anything.
	rctx, rcancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer rcancel()
	if _, err := kubectl(rctx, "-n", "admin", "rollout", "status", "deployment/admin", "--timeout=3m"); err != nil {
		t.Fatal(err)
	}
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	k := loadServiceKey(t, adminAppKeySecret, adminAppKeyField)
	since := time.Now().Add(-time.Second).UTC().Format(time.RFC3339)

	r := zitadelCall(t, iamAdmin, http.MethodPost, "/management/v1/users/"+k.UserID+"/pats", map[string]any{
		"expirationDate": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	mustStatus(t, r, http.StatusOK, "add a personal access token to admin-app")
	var pat struct {
		TokenID string `json:"tokenId"`
		Token   string `json:"token"`
	}
	r.decode(t, &pat)
	t.Cleanup(func() {
		// Minutes later: the first token (60 s on kind) has expired.
		iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
		r := zitadelCall(t, iamAdmin, http.MethodDelete, "/management/v1/users/"+k.UserID+"/pats/"+pat.TokenID, nil)
		mustStatus(t, r, http.StatusOK, "remove the test personal access token")
	})

	// The check runs every 5 minutes.
	deadline := time.Now().Add(6 * time.Minute)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		logs, err := kubectl(ctx, "-n", "admin", "logs", "-l", "app.kubernetes.io/name=admin",
			"--since-time="+since, "--tail=-1")
		cancel()
		if err != nil {
			// A pod that is still starting has no logs yet; try again.
			logs = ""
			t.Logf("reading the admin app's logs: %v", err)
		}
		if pat.Token != "" && strings.Contains(logs, pat.Token) {
			t.Fatal("the admin app logged the personal access token itself")
		}
		for _, line := range strings.Split(logs, "\n") {
			var e struct {
				Level, Msg, Account, Credential string
				CredentialID                    string `json:"credential_id"`
			}
			if json.Unmarshal([]byte(line), &e) != nil || e.Msg != driftEvent || e.CredentialID != pat.TokenID {
				continue
			}
			if e.Level != "ERROR" || e.Account != "admin-app" || e.Credential != "personal_access_token" {
				t.Errorf("drift event = %+v, want ERROR for admin-app's personal_access_token", e)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s event for personal access token %s within 6 minutes", driftEvent, pat.TokenID)
		}
		time.Sleep(15 * time.Second)
	}
}
