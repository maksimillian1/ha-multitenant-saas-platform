package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// provisionTenant creates a tenant through Tenant Management and deletes it
// when the test ends; it returns the tenant's ID.
func provisionTenant(t *testing.T, prefix string) string {
	t.Helper()
	out := tmCall(t, "POST", "/tenants", `{"name":"`+prefix+`-`+randomID()+`","admin_email":"`+prefix+`-`+randomID()+`@example.com"}`)
	if !strings.Contains(out, "status=201") {
		t.Fatalf("create tenant: %s", out)
	}
	var created struct {
		ID string `json:"id"`
	}
	// The pod's output may carry more than the body; the body is its first JSON value.
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&created); err != nil || created.ID == "" {
		t.Fatalf("create tenant: %s, %v", out, err)
	}
	id := created.ID
	t.Cleanup(func() {
		tmCall(t, "POST", "/tenants/"+id+"/suspend", "")
		if out := tmCall(t, "DELETE", "/tenants/"+id, ""); !strings.Contains(out, "status=200") {
			t.Errorf("delete tenant %s: %s", id, out)
		}
	})
	return id
}

// The tenant namespaces' ResourceQuota rejects pods without requests and limits.
func podOverrides(name string, command []string) string {
	b, _ := json.Marshal(map[string]any{"spec": map[string]any{"containers": []map[string]any{{
		"name": name, "image": "busybox:1.37", "command": command,
		"resources": map[string]any{
			"requests": map[string]string{"cpu": "10m", "memory": "16Mi"},
			"limits":   map[string]string{"cpu": "10m", "memory": "16Mi"},
		},
	}}}})
	return string(b)
}

// tenantServer starts an HTTP server pod in namespace ns and returns its IP.
func tenantServer(t *testing.T, ns string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	name := "e2e-server-" + randomID()
	if _, err := kubectl(ctx, "-n", ns, "run", name, "--restart=Never", "--image=busybox:1.37",
		"--overrides="+podOverrides(name, []string{"sh", "-c", "mkdir -p /tmp/w && echo ok > /tmp/w/index.html && exec httpd -f -p 8080 -h /tmp/w"})); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), time.Minute)
		defer dcancel()
		_, _ = kubectl(dctx, "-n", ns, "delete", "pod", name, "--wait=false")
	})
	if _, err := kubectl(ctx, "-n", ns, "wait", "--for=condition=Ready", "pod/"+name, "--timeout=150s"); err != nil {
		t.Fatal(err)
	}
	ip, err := retryKubectl(ctx, "-n", ns, "get", "pod", name, "-o", "jsonpath={.status.podIP}")
	if err != nil || ip == "" {
		t.Fatalf("server pod IP: %q, %v", ip, err)
	}
	return strings.TrimSpace(ip)
}

// connects reports whether a pod in namespace ns gets an HTTP answer from ip.
func connects(t *testing.T, ns, ip string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	name := "e2e-probe-" + randomID()
	script := fmt.Sprintf(`if wget -q -T 5 -O /dev/null http://%s:8080/; then echo net=open; else echo net=blocked; fi`, ip)
	out, err := kubectl(ctx, "-n", ns, "run", name, "--rm", "-i", "--restart=Never", "--quiet", "--image=busybox:1.37",
		"--overrides="+podOverrides(name, []string{"sh", "-c", script}))
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case strings.Contains(out, "net=open"):
		return true
	case strings.Contains(out, "net=blocked"):
		return false
	}
	t.Fatalf("probe from %s to %s: unexpected output %q", ns, ip, out)
	return false
}

// A pod in a tenant's namespace cannot connect to a pod of another tenant,
// and pods outside tenant namespaces cannot connect in. The same-namespace
// probes are the control: they show the server answers and the probe works.
//
// Verifies: tenant-management AC-19; QS-02; T-01, T-12
func TestTenantNamespacesAreIsolated(t *testing.T) {
	nsA := "tenant-" + provisionTenant(t, "e2e-net")
	nsB := "tenant-" + provisionTenant(t, "e2e-net")
	ipA, ipB := tenantServer(t, nsA), tenantServer(t, nsB)

	for _, tc := range []struct {
		name, from, to string
		open           bool
	}{
		{"A to A", nsA, ipA, true},
		{"B to B", nsB, ipB, true},
		{"B to A", nsB, ipA, false},
		{"A to B", nsA, ipB, false},
		{"default to A", "default", ipA, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := connects(t, tc.from, tc.to); got != tc.open {
				t.Errorf("pod in %s to %s: open=%v, want %v", tc.from, tc.to, got, tc.open)
			}
		})
	}
}
