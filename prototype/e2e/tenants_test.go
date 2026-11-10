package e2e

import (
	"context"
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
