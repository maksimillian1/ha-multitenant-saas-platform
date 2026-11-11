package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// tenantsProbe calls Tenant Management without a token: 401 means the pod
// got through the network policy (the token check answered), no answer
// means the policy dropped it.
const tenantsProbe = `
if code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' http://tenants.tenants.svc:8080/tenants); then echo "tenants=$code"; else echo tenants=blocked; fi`

// HAMSP-51 AC-10: only pods of the admin app (namespace admin, label
// app.kubernetes.io/name=admin) can connect to Tenant Management. The
// control pod carries the admin app's label and must get the token check's
// 401, so a blocked answer elsewhere is the policy, not a broken service.
//
// Verifies: tenant-management AC-10; T-12
func TestOnlyAdminNamespaceReachesTenantManagement(t *testing.T) {
	if out := directAccess(t, "admin", "app.kubernetes.io/name=admin", tenantsProbe); !strings.Contains(out, "tenants=401") {
		t.Fatalf("a pod with the admin app's label did not get 401 from Tenant Management, so the check proves nothing: %q", out)
	}
	for _, tc := range []struct{ ns, labels string }{
		{"default", ""},
		{"exampleapp", ""},
		{"tenants", ""},
		// The namespace alone is not enough: other pods in admin are refused.
		{"admin", "app.kubernetes.io/name=not-admin"},
		// The label alone is not enough either.
		{"default", "app.kubernetes.io/name=admin"},
	} {
		t.Run(tc.ns+"/"+tc.labels, func(t *testing.T) {
			if out := directAccess(t, tc.ns, tc.labels, tenantsProbe); !strings.Contains(out, "tenants=blocked") {
				t.Fatalf("pod in %s with labels %q reached Tenant Management: %q", tc.ns, tc.labels, out)
			}
		})
	}
}

// Tenant Management's own machine user has only the key the bootstrap gave
// it, valid at most 30 days, and no personal access token; its Secret holds
// the key and the audience, no token.
//
// Verifies: tenant-management AC-16
func TestTenantManagementHasOnlyShortKey(t *testing.T) {
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	k := loadServiceKey(t, "tenants/tenants-zitadel", "key.json")
	checkOnlyShortKey(t, iamAdmin, "tenant-management", k.UserID, k.KeyID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := secretField(ctx, "tenants/tenants-zitadel", "ZITADEL_TOKEN"); err == nil {
		t.Error("Secret tenants/tenants-zitadel still has ZITADEL_TOKEN, want only the key")
	}
}

// tmCall calls Tenant Management from a pod with the admin app's label, the
// only pod its network policy lets in, with the admin app's client token.
func tmCall(t *testing.T, method, path, body string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	field := func(f string) string {
		v, err := secretField(ctx, "admin/admin-tenants", f)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tok, err := fetchTokenWithScope(tenant{Name: "admin-tenants-client", ClientID: field("TENANTS_CLIENT_ID"), ClientSecret: field("TENANTS_CLIENT_SECRET")},
		"openid urn:zitadel:iam:org:project:id:"+field("TENANTS_AUDIENCE")+":aud urn:zitadel:iam:org:projects:roles")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`curl -s -m 60 -X %s -H 'Authorization: Bearer %s' -H 'X-Admin-User-Id: e2e' -H 'Content-Type: application/json' --data '%s' -w '\nstatus=%%{http_code}' http://tenants.tenants.svc:8080%s`,
		method, tok, body, path)
	return directAccess(t, "admin", "app.kubernetes.io/name=admin", script)
}

// HAMSP-52: a suspended tenant loses API access within seconds, through the
// tenant-states topic, not only when its tokens expire; resume gives it back.
//
// Verifies: tenant-management AC-17
func TestSuspendedTenantLosesAPIAccess(t *testing.T) {
	out := tmCall(t, "POST", "/tenants", `{"name":"e2e-suspend-`+randomID()+`","admin_email":"e2e-suspend@example.com"}`)
	if !strings.Contains(out, "status=201") {
		t.Fatalf("create tenant: %s", out)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out[:strings.LastIndex(out, "\nstatus=")]), &created); err != nil || created.ID == "" {
		t.Fatalf("create tenant: %s, %v", out, err)
	}
	id := created.ID
	t.Cleanup(func() {
		tmCall(t, "POST", "/tenants/"+id+"/suspend", "")
		if out := tmCall(t, "DELETE", "/tenants/"+id, ""); !strings.Contains(out, "status=200") {
			t.Errorf("delete tenant %s: %s", id, out)
		}
	})

	// A machine user of the new tenant, made by iam-admin like a tenant admin would.
	iamAdmin := keyToken(t, loadServiceKey(t, iamAdminKeySecret, iamAdminKeyField))
	username := "e2e-client-" + randomID()
	r := zitadelCall(t, iamAdmin, "POST", "/zitadel.user.v2.UserService/CreateUser", map[string]any{
		"organizationId": id, "username": username,
		"machine": map[string]any{"name": "e2e API client", "accessTokenType": "ACCESS_TOKEN_TYPE_JWT"},
	})
	mustStatus(t, r, http.StatusOK, "create machine user")
	var user struct {
		ID string `json:"id"`
	}
	r.decode(t, &user)
	r = zitadelCall(t, iamAdmin, "POST", "/zitadel.user.v2.UserService/AddSecret", map[string]any{"userId": user.ID})
	mustStatus(t, r, http.StatusOK, "add client secret")
	var secret struct {
		ClientSecret string `json:"clientSecret"`
	}
	r.decode(t, &secret)
	ten := tenant{Name: "e2e-suspend", OrgID: id, ClientID: username, ClientSecret: secret.ClientSecret}

	old := token(t, ten)
	mustStatus(t, call(t, "GET", "/items", old, nil), http.StatusOK, "active tenant")

	if out := tmCall(t, "POST", "/tenants/"+id+"/suspend", ""); !strings.Contains(out, "status=200") {
		t.Fatalf("suspend: %s", out)
	}
	suspended := time.Now()
	for status := 0; status != http.StatusForbidden; {
		if time.Since(suspended) > 10*time.Second {
			t.Fatalf("token issued before the suspension: status %d after 10 s, want 403", status)
		}
		time.Sleep(500 * time.Millisecond)
		status = call(t, "GET", "/items", old, nil).Status
	}
	if _, err := fetchToken(ten); err == nil {
		t.Error("a suspended tenant got a new token")
	}

	if out := tmCall(t, "POST", "/tenants/"+id+"/resume", ""); !strings.Contains(out, "status=200") {
		t.Fatalf("resume: %s", out)
	}
	resumed := time.Now()
	for status := 0; status != http.StatusOK; {
		if time.Since(resumed) > 10*time.Second {
			t.Fatalf("after resume: status %d after 10 s, want 200", status)
		}
		time.Sleep(500 * time.Millisecond)
		tok, err := fetchToken(ten)
		if err != nil {
			continue
		}
		status = call(t, "GET", "/items", tok, nil).Status
	}
}
