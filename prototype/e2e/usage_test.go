package e2e

// Usage pipeline (HAMSP-26): API Gateway → UsageWriteAPI → Kafka → Usage Writer
// → the usage database. Events are found by their path (a probe UUID per test)
// or their event_id, as the superuser, which bypasses RLS.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// usageRows returns how many usage events have the given path.
func usageRows(t testing.TB, path string) int {
	t.Helper()
	out, err := psqlDB(t, "usage", fmt.Sprintf("SELECT count(*) FROM usage_events WHERE attributes->>'path' = '%s'", path))
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("count: %q", out)
	}
	return n
}

// waitForUsageRows waits until want events with the path are stored, then
// checks that no more arrive.
func waitForUsageRows(t testing.TB, path string, want int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		n := usageRows(t, path)
		if n == want {
			break
		}
		if n > want || time.Now().After(deadline) {
			t.Fatalf("%s: %d usage events, want %d", path, n, want)
		}
		time.Sleep(time.Second)
	}
	time.Sleep(3 * time.Second)
	if n := usageRows(t, path); n != want {
		t.Fatalf("%s: %d usage events after settling, want %d", path, n, want)
	}
}

// waitForUsagePaths waits until each path has exactly one usage event, then
// checks that no more arrive. One query per poll, so hundreds of paths are
// checked in seconds.
func waitForUsagePaths(t testing.TB, paths []string, within time.Duration) {
	t.Helper()
	count := func() (rows, distinct int) {
		out, err := psqlDB(t, "usage", "SELECT count(*) || ' ' || count(DISTINCT attributes->>'path') FROM usage_events WHERE attributes->>'path' IN ("+idList(paths)+")")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Sscan(out, &rows, &distinct); err != nil {
			t.Fatalf("count: %q", out)
		}
		return rows, distinct
	}
	deadline := time.Now().Add(within)
	for {
		rows, distinct := count()
		if rows > distinct {
			t.Fatalf("%d usage events for %d paths: some calls are stored twice", rows, distinct)
		}
		if distinct == len(paths) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d calls stored", distinct, len(paths))
		}
		time.Sleep(time.Second)
	}
	time.Sleep(3 * time.Second)
	if rows, _ := count(); rows != len(paths) {
		t.Fatalf("%d usage events for %d calls after settling", rows, len(paths))
	}
}

func idList(ids []string) string {
	return "'" + strings.Join(ids, "','") + "'"
}

// storedIDs returns how many of the event IDs are stored.
func storedIDs(t testing.TB, ids []string) int {
	t.Helper()
	if len(ids) == 0 {
		return 0
	}
	out, err := psqlDB(t, "usage", "SELECT count(*) FROM usage_events WHERE event_id::text IN ("+idList(ids)+")")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(out)
	return n
}

func waitForStoredIDs(t testing.TB, ids []string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		n := storedIDs(t, ids)
		if n == len(ids) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d acknowledged events stored", n, len(ids))
		}
		time.Sleep(2 * time.Second)
	}
}

// writeAPI forwards a local port to UsageWriteAPI and returns its events URL.
// Port forwarding enters the pod's network namespace, so the network policy
// that admits only the API Gateway does not apply to the test.
func writeAPI(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "kubectl", "-n", "usage", "port-forward", "svc/usage-writeapi", ":8080")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	// "Forwarding from 127.0.0.1:43211 -> 8080"
	out := bufio.NewReader(stdout)
	line, err := out.ReadString('\n')
	if err != nil {
		t.Fatalf("port-forward: %v", err)
	}
	fields := strings.Fields(line)
	if len(fields) < 3 {
		t.Fatalf("port-forward: %q", line)
	}
	if _, _, err := net.SplitHostPort(fields[2]); err != nil {
		t.Fatalf("port-forward: %q", line)
	}
	go func() { _, _ = io.Copy(io.Discard, out) }()
	return "http://" + fields[2] + "/v1/events"
}

type usageEvent struct {
	ID         string         `json:"event_id"`
	Time       string         `json:"event_time"`
	TenantID   string         `json:"tenant_id"`
	Metric     string         `json:"metric"`
	Quantity   int64          `json:"quantity"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

func newUsageEvent(tenantID, path string) usageEvent {
	return usageEvent{
		ID: newUUID(), Time: time.Now().UTC().Format(time.RFC3339Nano), TenantID: tenantID,
		Metric: "api_calls", Quantity: 1, Attributes: map[string]any{"method": "GET", "path": path, "status": 200},
	}
}

var direct = &http.Client{Timeout: 15 * time.Second}

// sendUsage posts events to UsageWriteAPI and returns the status code.
func sendUsage(url string, events ...usageEvent) (int, error) {
	raw, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return 0, err
	}
	resp, err := direct.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// gatewayMetric sums a counter over all API Gateway pods, read through the API
// server's pod proxy (the metrics port is not routed by the public Gateway).
func gatewayMetric(t testing.TB, name string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pods, err := kubectl(ctx, "-n", "gateway", "get", "pods", "-l", "app.kubernetes.io/name=api-gateway",
		"--field-selector=status.phase=Running", "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, pod := range strings.Fields(pods) {
		out, err := kubectl(ctx, "get", "--raw", "/api/v1/namespaces/gateway/pods/"+pod+":9090/proxy/metrics")
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(out, "\n") {
			if v, ok := strings.CutPrefix(line, name+" "); ok {
				n, _ := strconv.ParseInt(v, 10, 64)
				sum += n
			}
		}
	}
	return sum
}

// applyTemporary applies a manifest for the duration of fn. ArgoCD does not
// prune it: it carries no ArgoCD tracking.
func applyTemporary(t *testing.T, manifest string) (remove func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := kubectlStdin(ctx, manifest, "apply", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	remove = func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if _, err := kubectlStdin(ctx, manifest, "delete", "--ignore-not-found", "-f", "-"); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(remove)
	return remove
}

// deletePods deletes the pods with the label and waits until their deployment
// is available again.
func deletePods(t *testing.T, ns, selector, deployment string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := kubectl(ctx, "-n", ns, "delete", "pods", "-l", selector, "--wait=false"); err != nil {
		t.Fatal(err)
	}
	if deployment != "" {
		time.Sleep(2 * time.Second)
		if _, err := kubectl(ctx, "-n", ns, "rollout", "status", "deployment/"+deployment, "--timeout=150s"); err != nil {
			t.Fatal(err)
		}
	}
}

// AC-1: an API call through the Gateway is stored as exactly one api_calls
// event of the caller's tenant, with request data but no query string.
func TestAPICallIsStoredAsUsageEvent(t *testing.T) {
	for _, ten := range []tenant{tenA, tenB} {
		t.Run(ten.Name, func(t *testing.T) {
			probe := newUUID()
			path := "/items/" + probe
			mustStatus(t, call(t, http.MethodGet, path+"?note=secret-"+probe, token(t, ten), nil), http.StatusNotFound, "GET unknown item")
			waitForUsageRows(t, path, 1, 30*time.Second)

			out, err := psqlDB(t, "usage", fmt.Sprintf(`
				SELECT concat_ws('|', tenant_id, metric, quantity, attributes->>'method', attributes->>'status',
				                 attributes ?& array['duration_ms', 'request_bytes', 'response_bytes'],
				                 attributes::text LIKE '%%secret%%')
				FROM usage_events WHERE attributes->>'path' = '%s'`, path))
			if err != nil {
				t.Fatal(err)
			}
			if want := ten.OrgID + "|api_calls|1|GET|404|t|f"; out != want {
				t.Fatalf("tenant|metric|quantity|method|status|has duration and bytes|leaks query = %q, want %q", out, want)
			}
		})
	}
}

// AC-2: a request the Gateway rejects with 401 is not stored.
func TestRejectedRequestHasNoUsageEvent(t *testing.T) {
	probe, control := "/items/"+newUUID(), "/items/"+newUUID()
	mustStatus(t, call(t, http.MethodGet, probe, "", nil), http.StatusUnauthorized, "no token")
	mustStatus(t, call(t, http.MethodGet, probe, "not-a-jwt", nil), http.StatusUnauthorized, "garbage token")
	// The control request is sent after the probes; once it is stored, the
	// probes would have been too.
	mustStatus(t, call(t, http.MethodGet, control, token(t, tenA), nil), http.StatusNotFound, "control")
	waitForUsageRows(t, control, 1, 30*time.Second)
	if n := usageRows(t, probe); n != 0 {
		t.Fatalf("%d usage events for requests rejected with 401", n)
	}
}

// AC-3: while UsageWriteAPI cannot be reached, API calls still succeed, and
// once it is back every call made meanwhile is stored exactly once.
func TestUsageSurvivesUsageWriteAPIOutage(t *testing.T) {
	if testing.Short() {
		t.Skip("takes over a minute")
	}
	// A deny rule beats the allow rule of usage-writeapi-from-gateway-only.
	// Deleting the pods drops the Gateway's open connections, which a new
	// policy would not cut.
	restore := applyTemporary(t, `
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: e2e-usage-writeapi-outage
  namespace: usage
spec:
  endpointSelector:
    matchLabels:
      app.kubernetes.io/name: usage-writeapi
  ingressDeny:
    - fromEndpoints:
        - matchLabels:
            k8s:io.kubernetes.pod.namespace: gateway
            app.kubernetes.io/name: api-gateway
`)
	deletePods(t, "usage", "app.kubernetes.io/name=usage-writeapi", "usage-writeapi")
	failedBefore := gatewayMetric(t, "usage_events_failed_attempts_total")

	tok := token(t, tenA)
	var paths []string
	start := time.Now()
	for i := 0; i < 20; i++ {
		p := "/items/" + newUUID()
		mustStatus(t, call(t, http.MethodGet, p, tok, nil), http.StatusNotFound, "API call during the outage")
		paths = append(paths, p)
	}
	time.Sleep(time.Until(start.Add(60 * time.Second)))
	if gatewayMetric(t, "usage_events_failed_attempts_total") == failedBefore {
		t.Fatal("the Gateway never failed to reach UsageWriteAPI; the outage did not happen")
	}
	for _, p := range paths {
		if n := usageRows(t, p); n != 0 {
			t.Fatalf("%s stored during the outage", p)
		}
	}

	restore()
	waitForUsagePaths(t, paths, 90*time.Second)
}

// directAccess runs a curl pod in ns with the given labels and returns its output.
func directAccess(t *testing.T, ns, labels, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args := []string{"-n", ns, "run", "e2e-direct-" + randomID(), "--rm", "-i", "--restart=Never", "--quiet",
		"--image=curlimages/curl:8.11.1"}
	if labels != "" {
		args = append(args, "--labels="+labels)
	}
	out, err := kubectl(ctx, append(args, "--command", "--", "sh", "-c", script)...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// AC-7: pods other than the API Gateway cannot connect to UsageWriteAPI.
func TestDirectAccessToUsageWriteAPIIsBlocked(t *testing.T) {
	for _, ns := range []string{"default", "usage"} {
		t.Run(ns, func(t *testing.T) {
			out := directAccess(t, ns, "", `
code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' http://api-gateway.gateway.svc:8080/items)
echo "gateway=$code"
if curl -s -m 5 -o /dev/null http://usage-writeapi.usage.svc:8080/healthz; then echo direct=open; else echo direct=blocked; fi`)
			if !strings.Contains(out, "gateway=401") {
				t.Fatalf("control request to the gateway failed, so the check proves nothing: %q", out)
			}
			if !strings.Contains(out, "direct=blocked") {
				t.Fatalf("pod in %s reached UsageWriteAPI directly: %q", ns, out)
			}
		})
	}
}

// AC-8: only UsageWriteAPI and Usage Writer can connect to the Kafka client
// listener. The control pod carries Usage Writer's label and gets through.
func TestDirectAccessToKafkaIsBlocked(t *testing.T) {
	// time_connect is 0 when the TCP connection was never established.
	script := `curl -s -m 8 --connect-timeout 4 -o /dev/null -w 'connect=%{time_connect}' telnet://usage-kafka-kafka-bootstrap.kafka.svc:9092 </dev/null; true`
	connected := func(out string) bool {
		v, ok := strings.CutPrefix(strings.TrimSpace(out), "connect=")
		f, err := strconv.ParseFloat(v, 64)
		return ok && err == nil && f > 0
	}
	if out := directAccess(t, "usage", "app.kubernetes.io/name=usage-writer", script); !connected(out) {
		t.Fatalf("a pod with Usage Writer's label could not connect, so the check proves nothing: %q", out)
	}
	for _, ns := range []string{"default", "usage"} {
		if out := directAccess(t, ns, "", script); connected(out) {
			t.Fatalf("pod in %s connected to Kafka: %q", ns, out)
		}
	}
}

// AC-9 / QS-06: the same event sent to UsageWriteAPI 3 times is stored once.
func TestDuplicateEventIsStoredOnce(t *testing.T) {
	url := writeAPI(t)
	path := "/e2e/dup/" + newUUID()
	e := newUsageEvent(tenA.OrgID, path)
	for i := 0; i < 3; i++ {
		code, err := sendUsage(url, e)
		if err != nil || code != http.StatusAccepted {
			t.Fatalf("send %d: status %d, err %v", i, code, err)
		}
	}
	waitForUsageRows(t, path, 1, 30*time.Second)
}

// AC-10 / QS-06: Usage Writer pods are killed while events flow; every event
// ends up stored exactly once.
func TestUsageWriterRestartsStoreOnce(t *testing.T) {
	url := writeAPI(t)
	var ids []string
	for round := 0; round < 3; round++ {
		for i := 0; i < 5; i++ {
			batch := make([]usageEvent, 20)
			for j := range batch {
				batch[j] = newUsageEvent([]string{tenA.OrgID, tenB.OrgID}[j%2], "/e2e/restart")
				ids = append(ids, batch[j].ID)
			}
			if code, err := sendUsage(url, batch...); err != nil || code != http.StatusAccepted {
				t.Fatalf("status %d, err %v", code, err)
			}
		}
		deletePods(t, "usage", "app.kubernetes.io/name=usage-writer", "")
		time.Sleep(3 * time.Second)
	}
	waitForStoredIDs(t, ids, 3*time.Minute)
	// Duplicates cannot exist under the primary key; the count proves none was lost.
	if n := storedIDs(t, ids); n != len(ids) {
		t.Fatalf("%d rows, want %d", n, len(ids))
	}
}

// AC-11: usage_events has forced RLS; Usage Writer connects as a role that does
// not own it, is NOSUPERUSER NOBYPASSRLS, and sees nothing without a tenant.
func TestUsageTableRLSAndWriterRole(t *testing.T) {
	out, err := psqlDB(t, "usage", `
		SELECT concat_ws('|', c.relrowsecurity, c.relforcerowsecurity, pg_get_userbyid(c.relowner),
		                 r.rolsuper, r.rolbypassrls)
		FROM pg_class c, pg_roles r
		WHERE c.oid = 'public.usage_events'::regclass AND r.rolname = 'usage_writer'`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "t|t|usage_owner|f|f"; out != want {
		t.Fatalf("rowsecurity|forcerowsecurity|owner|rolsuper|rolbypassrls = %q, want %q", out, want)
	}
	users, err := psqlDB(t, "usage", `
		SELECT string_agg(DISTINCT usename, ',') FROM pg_stat_activity
		WHERE datname = 'usage' AND backend_type = 'client backend' AND pid <> pg_backend_pid()
		  AND usename NOT IN ('cnpg_pooler_pgbouncer', 'postgres', 'usage_owner')`)
	if err != nil {
		t.Fatal(err)
	}
	if users != "usage_writer" {
		t.Fatalf("roles connected to the usage database = %q, want only usage_writer", users)
	}
	n, err := psqlDB(t, "usage", "SET ROLE usage_writer; SELECT count(*) FROM usage_events")
	if err != nil {
		t.Fatal(err)
	}
	if n != "0" {
		t.Fatalf("SELECT without tenant context returned %s rows, want 0", n)
	}
	_, err = psqlDB(t, "usage", "SET ROLE usage_writer; INSERT INTO usage_events (event_id, event_time, tenant_id, metric, quantity) VALUES (gen_random_uuid(), now(), '"+tenA.OrgID+"', 'api_calls', 1)")
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("INSERT without tenant context: err = %v, want a row-level security violation", err)
	}
}

// AC-13 / QS-09: the usage database is unreachable for Usage Writer for 5
// minutes while API calls keep coming; UsageWriteAPI keeps accepting, and
// afterwards every call is stored once.
func TestUsageSurvivesDatabaseOutage(t *testing.T) {
	if testing.Short() {
		t.Skip("takes over 5 minutes")
	}
	// No other policy selects the pooler, and a policy with only ingressDeny
	// rules turns on default deny for ingress, which would cut ExampleApp off
	// too. enableDefaultDeny keeps the deny limited to Usage Writer.
	restore := applyTemporary(t, `
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: e2e-usage-db-outage
  namespace: database
spec:
  endpointSelector:
    matchLabels:
      cnpg.io/poolerName: main-pg-cluster-pooler-rw
  enableDefaultDeny:
    ingress: false
  ingressDeny:
    - fromEndpoints:
        - matchLabels:
            k8s:io.kubernetes.pod.namespace: usage
            app.kubernetes.io/name: usage-writer
`)
	// New Usage Writer pods, so no connection opened before the deny survives.
	deletePods(t, "usage", "app.kubernetes.io/name=usage-writer", "")
	failedBefore := gatewayMetric(t, "usage_events_failed_attempts_total")
	droppedBefore := gatewayMetric(t, "usage_events_dropped_total")

	tok := token(t, tenA)
	var paths []string
	end := time.Now().Add(5 * time.Minute)
	for time.Now().Before(end) {
		p := "/items/" + newUUID()
		r, err := doCall(http.MethodGet, p, tok, nil)
		if err != nil || r.Status != http.StatusNotFound {
			// The token lives 60 s (bootstrap-tenants.sh).
			tok = token(t, tenA)
			continue
		}
		paths = append(paths, p)
		time.Sleep(time.Second)
	}
	if len(paths) == 0 {
		t.Fatal("no API call succeeded during the outage")
	}
	if storedPaths := usageRows(t, paths[0]) + usageRows(t, paths[len(paths)-1]); storedPaths != 0 {
		t.Fatal("events were stored during the outage; the database was not unreachable")
	}
	if got := gatewayMetric(t, "usage_events_failed_attempts_total"); got != failedBefore {
		t.Fatalf("the Gateway saw %d failed sends during the outage; UsageWriteAPI must keep accepting", got-failedBefore)
	}
	if got := gatewayMetric(t, "usage_events_dropped_total"); got != droppedBefore {
		t.Fatalf("%d events dropped by the Gateway", got-droppedBefore)
	}

	restore()
	out, err := psqlDB(t, "usage", "SELECT 1") // the database itself was never down
	if err != nil || out != "1" {
		t.Fatalf("usage database: %q, %v", out, err)
	}
	waitForUsagePaths(t, paths, 3*time.Minute)
}

// AC-14 / QS-17: one Kafka broker pod is deleted while events flow; every
// event UsageWriteAPI acknowledged is stored.
func TestBrokerLossLosesNoAcknowledgedEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("takes over a minute")
	}
	url := writeAPI(t)
	var mu sync.Mutex
	var acked []string
	var failed int
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				e := newUsageEvent(tenA.OrgID, "/e2e/broker-loss")
				code, err := sendUsage(url, e)
				mu.Lock()
				if err == nil && code == http.StatusAccepted {
					acked = append(acked, e.ID)
				} else {
					failed++
				}
				mu.Unlock()
				time.Sleep(200 * time.Millisecond)
			}
		}()
	}
	time.Sleep(10 * time.Second)
	kctx, kcancel := context.WithTimeout(context.Background(), time.Minute)
	if _, err := kubectl(kctx, "-n", "kafka", "delete", "pod", "usage-kafka-dual-role-0", "--wait=false"); err != nil {
		t.Error(err)
	}
	kcancel()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	t.Logf("%d acknowledged, %d failed (%.1f%%) while a broker was replaced", len(acked), failed,
		100*float64(failed)/float64(max(1, len(acked)+failed)))
	if len(acked) == 0 {
		t.Fatal("no event was acknowledged")
	}
	waitForStoredIDs(t, acked, 3*time.Minute)
}
