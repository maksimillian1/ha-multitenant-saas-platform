package e2e

import (
	"strings"
	"testing"
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
