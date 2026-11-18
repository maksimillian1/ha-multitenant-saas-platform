package e2e

// Kafka client authentication and ACLs (HAMSP-31, feature kafka-client-auth).
// The checks run the Kafka CLI of the Strimzi image in a pod that carries an
// allowed pod label, so the network policy lets it through and only the login
// and the ACLs decide.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	kafkaCLIImage = "quay.io/strimzi/kafka:1.3.0-kafka-4.3.1"
	kafkaTLS      = "usage-kafka-kafka-bootstrap.kafka.svc:9093"
	kafkaBin      = "/opt/kafka/bin"
)

// kafkaLogin is what a client presents: the protocol, and for SASL a user.
type kafkaLogin struct {
	protocol string // SASL_SSL, SSL or PLAINTEXT
	user     string
	password string
}

// properties is the client config for login, with the cluster CA inline.
func (l kafkaLogin) properties(ca string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "security.protocol=%s\n", l.protocol)
	if l.protocol == "SASL_SSL" {
		b.WriteString("sasl.mechanism=SCRAM-SHA-512\n")
		fmt.Fprintf(&b, "sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username=\"%s\" password=\"%s\";\n", l.user, l.password)
	}
	if l.protocol != "PLAINTEXT" {
		b.WriteString("ssl.truststore.type=PEM\n")
		b.WriteString("ssl.truststore.certificates=" + strings.ReplaceAll(strings.TrimSpace(ca), "\n", " \\\n") + "\n")
	}
	// Fail fast instead of retrying for minutes.
	b.WriteString("request.timeout.ms=10000\ndefault.api.timeout.ms=20000\n")
	return b.String()
}

// kafkaCA is the cluster CA the clients of namespace usage trust.
func kafkaCA(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ca, err := secretField(ctx, "usage/usage-kafka-ca", "ca.crt")
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

// kafkaUser is the SCRAM login of a usage binary, from its Secret.
func kafkaUser(t *testing.T, secret string) kafkaLogin {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user, err := secretField(ctx, "usage/"+secret, "KAFKA_USERNAME")
	if err != nil {
		t.Fatal(err)
	}
	password, err := secretField(ctx, "usage/"+secret, "KAFKA_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	return kafkaLogin{protocol: "SASL_SSL", user: user, password: password}
}

// kafkaStep is one Kafka CLI call. Its output is printed between markers, so a
// check reads only its own step.
type kafkaStep struct {
	name  string
	login kafkaLogin
	cmd   string // a CLI call; $C is the client config file
	stdin string
}

// runKafkaSteps runs the steps in one pod in namespace usage with Usage
// Writer's label and returns the output of each step.
func runKafkaSteps(t *testing.T, steps ...kafkaStep) map[string]string {
	t.Helper()
	ca := kafkaCA(t)
	var script strings.Builder
	for i, s := range steps {
		fmt.Fprintf(&script, "cat > /tmp/c%d.properties <<'EOF'\n%sEOF\n", i, s.login.properties(ca))
		fmt.Fprintf(&script, "echo '>>> %s'\n", s.name)
		cmd := strings.ReplaceAll(s.cmd, "$C", fmt.Sprintf("/tmp/c%d.properties", i))
		if s.stdin != "" {
			fmt.Fprintf(&script, "printf '%%s\\n' '%s' | timeout 90 %s 2>&1\n", s.stdin, cmd)
		} else {
			fmt.Fprintf(&script, "timeout 90 %s </dev/null 2>&1\n", cmd)
		}
		fmt.Fprintf(&script, "echo \"<<< %s rc=$?\"\n", s.name)
	}
	out := runPod(t, "usage", kafkaCLIImage, "app.kubernetes.io/name=usage-writer", script.String(), 10*time.Minute)
	res := map[string]string{}
	for _, s := range steps {
		_, rest, ok := strings.Cut(out, ">>> "+s.name+"\n")
		body, _, ok2 := strings.Cut(rest, "<<< "+s.name)
		if !ok || !ok2 {
			t.Fatalf("step %s has no output:\n%s", s.name, out)
		}
		res[s.name] = body
	}
	return res
}

// runPod runs script in a new pod and returns its log. It reads the log after
// the pod ended instead of attaching, which can duplicate or lose output.
func runPod(t *testing.T, ns, image, labels, script string, within time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	name := "e2e-kafka-" + randomID()
	args := []string{"-n", ns, "run", name, "--restart=Never", "--image=" + image, "--labels=" + labels,
		"--command", "--", "sh", "-c", script}
	if _, err := kubectl(ctx, args...); err != nil {
		t.Fatal(err)
	}
	defer func() {
		dctx, dcancel := context.WithTimeout(context.Background(), time.Minute)
		defer dcancel()
		_, _ = kubectl(dctx, "-n", ns, "delete", "pod", name, "--wait=false")
	}()
	for {
		phase, err := kubectl(ctx, "-n", ns, "get", "pod", name, "-o", "jsonpath={.status.phase}")
		if err != nil {
			t.Fatal(err)
		}
		if phase == "Succeeded" || phase == "Failed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pod %s did not finish within %s (phase %q)", name, within, phase)
		case <-time.After(2 * time.Second):
		}
	}
	out, err := kubectl(ctx, "-n", ns, "logs", name)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// usageRecord is a valid usage-events record of tenant A, key and value for
// the console producer. A record that Usage Writer cannot read would stop
// its partition, so even a record that should be denied is valid.
func usageRecord(t *testing.T, path string) (string, string) {
	t.Helper()
	e := newUsageEvent(tenA.OrgID, path)
	raw, err := json.Marshal(map[string]any{
		"schema_version": 1, "event_id": e.ID, "event_time": e.Time, "tenant_id": e.TenantID,
		"metric": e.Metric, "quantity": e.Quantity, "attributes": e.Attributes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e.ID, e.TenantID + "|" + string(raw)
}

func produceCmd(topic string) string {
	return kafkaBin + "/kafka-console-producer.sh --bootstrap-server " + kafkaTLS + " --topic " + topic +
		" --producer.config $C --property parse.key=true --property key.separator='|'" +
		" --producer-property max.block.ms=15000 --producer-property delivery.timeout.ms=20000"
}

// readCmd reads one record of partition 0 without a consumer group.
func readCmd(topic string) string {
	return kafkaBin + "/kafka-console-consumer.sh --bootstrap-server " + kafkaTLS + " --topic " + topic +
		" --partition 0 --offset earliest --max-messages 1 --timeout-ms 20000 --consumer.config $C"
}

// groupReadCmd reads with a consumer group.
func groupReadCmd(topic, group string) string {
	return kafkaBin + "/kafka-console-consumer.sh --bootstrap-server " + kafkaTLS + " --topic " + topic +
		" --group " + group + " --from-beginning --max-messages 1 --timeout-ms 20000 --consumer.config $C"
}

func mustContain(t *testing.T, out, want, what string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("%s: want %q in the output:\n%s", what, want, out)
	}
}

func mustNotContain(t *testing.T, out, bad, what string) {
	t.Helper()
	if strings.Contains(out, bad) {
		t.Errorf("%s: unexpected %q in the output:\n%s", what, bad, out)
	}
}

// Verifies: kafka-client-auth AC-1; T-04; T-12
func TestKafkaRejectsClientWithoutValidLogin(t *testing.T) {
	writeapi := kafkaUser(t, "usage-writeapi-kafka")
	wrong := writeapi
	wrong.password += "-wrong"
	controlID, controlRecord := usageRecord(t, "/e2e/kafka-auth/control")
	_, record := usageRecord(t, "/e2e/kafka-auth/denied")
	out := runKafkaSteps(t,
		// Control: the right login produces, so the denials below prove something.
		kafkaStep{name: "control", login: writeapi, cmd: produceCmd("usage-events"), stdin: controlRecord},
		kafkaStep{name: "wrong-password", login: wrong, cmd: produceCmd("usage-events"), stdin: record},
		kafkaStep{name: "tls-without-login", login: kafkaLogin{protocol: "SSL"}, cmd: produceCmd("usage-events"), stdin: record},
		kafkaStep{name: "no-tls", login: kafkaLogin{protocol: "PLAINTEXT"}, cmd: produceCmd("usage-events"), stdin: record},
	)
	mustNotContain(t, out["control"], "ERROR", "control produce with the usage-writeapi login")
	mustContain(t, out["control"], "rc=0", "control produce with the usage-writeapi login")
	mustContain(t, out["wrong-password"], "SaslAuthenticationException", "wrong password")
	// The listener expects a SASL handshake; a TLS client without SASL is cut off.
	mustContain(t, out["tls-without-login"], "ERROR", "TLS without a login")
	// A plaintext client cannot talk to a TLS listener at all.
	mustContain(t, out["no-tls"], "ERROR", "plaintext client")
	waitForStoredIDs(t, []string{controlID}, 2*time.Minute)
}

// Verifies: kafka-client-auth AC-3; T-04; T-12
func TestWriteAPIUserOnlyProducesUsageEvents(t *testing.T) {
	writeapi := kafkaUser(t, "usage-writeapi-kafka")
	id, record := usageRecord(t, "/e2e/kafka-auth/writeapi")
	out := runKafkaSteps(t,
		kafkaStep{name: "produce", login: writeapi, cmd: produceCmd("usage-events"), stdin: record},
		kafkaStep{name: "read", login: writeapi, cmd: readCmd("usage-events")},
		kafkaStep{name: "produce-other-topic", login: writeapi, cmd: produceCmd("tenant-states"), stdin: `e2e|{"tenant_id":"e2e"}`},
	)
	mustNotContain(t, out["produce"], "ERROR", "usage-writeapi produces to usage-events")
	mustContain(t, out["read"], "TopicAuthorizationException", "usage-writeapi reads usage-events")
	mustContain(t, out["produce-other-topic"], "TopicAuthorizationException", "usage-writeapi produces to tenant-states")
	waitForStoredIDs(t, []string{id}, 2*time.Minute)
}

// The writer's own group is checked with describe, not by consuming: a
// consumer in group usage-writer would take partitions from the running
// Usage Writer and commit offsets of events it never stores.
//
// Verifies: kafka-client-auth AC-4; T-04
func TestWriterUserOnlyConsumesWithItsGroup(t *testing.T) {
	writer := kafkaUser(t, "usage-writer-kafka")
	_, record := usageRecord(t, "/e2e/kafka-auth/writer")
	out := runKafkaSteps(t,
		kafkaStep{name: "read", login: writer, cmd: readCmd("usage-events")},
		kafkaStep{name: "own-group", login: writer, cmd: kafkaBin + "/kafka-consumer-groups.sh --bootstrap-server " + kafkaTLS +
			" --command-config $C --describe --group usage-writer"},
		kafkaStep{name: "other-group", login: writer, cmd: groupReadCmd("usage-events", "e2e-other-"+randomID())},
		kafkaStep{name: "produce", login: writer, cmd: produceCmd("usage-events"), stdin: record},
	)
	mustContain(t, out["read"], "Processed a total of 1 messages", "usage-writer reads usage-events")
	mustNotContain(t, out["own-group"], "Exception", "usage-writer describes its group")
	mustContain(t, out["own-group"], "usage-events", "usage-writer describes its group")
	mustContain(t, out["other-group"], "GroupAuthorizationException", "usage-writer consumes with another group")
	mustContain(t, out["produce"], "TopicAuthorizationException", "usage-writer produces to usage-events")
}

// Verifies: kafka-client-auth AC-5
func TestUsageMigrateHasNoKafkaCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	spec, err := kubectl(ctx, "-n", "usage", "get", "job", "usage-migrate", "-o", "jsonpath={.spec.template.spec}")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"KAFKA", "kafka"} {
		if strings.Contains(spec, bad) {
			t.Errorf("the migrate Job mentions %q: %s", bad, spec)
		}
	}
	users, err := kubectl(ctx, "-n", "kafka", "get", "kafkauser", "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(users, "usage-writer") {
		t.Fatalf("no KafkaUsers found, so the check proves nothing: %q", users)
	}
	if strings.Contains(users, "migrate") {
		t.Errorf("a KafkaUser for migrate exists: %q", users)
	}
}

// Verifies: kafka-client-auth AC-2
func TestKafkaHasOnlyTheTLSListener(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := kubectl(ctx, "-n", "kafka", "get", "kafka", "usage-kafka", "-o", "jsonpath={.spec.kafka.listeners}")
	if err != nil {
		t.Fatal(err)
	}
	var listeners []struct {
		Name           string         `json:"name"`
		Port           int            `json:"port"`
		TLS            bool           `json:"tls"`
		Authentication map[string]any `json:"authentication"`
	}
	if err := json.Unmarshal([]byte(out), &listeners); err != nil {
		t.Fatal(err)
	}
	if len(listeners) != 1 {
		t.Fatalf("want one client listener, got %+v", listeners)
	}
	l := listeners[0]
	if l.Port != 9093 || !l.TLS || l.Authentication["type"] != "scram-sha-512" {
		t.Errorf("listener %+v, want port 9093 with TLS and scram-sha-512", l)
	}
	for _, d := range []string{"usage/usage-writeapi", "usage/usage-writer", "gateway/api-gateway", "tenants/tenants"} {
		ns, name, _ := strings.Cut(d, "/")
		env, err := kubectl(ctx, "-n", ns, "get", "deploy", name, "-o", "jsonpath={.spec.template.spec.containers[0].env}")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(env, kafkaTLS) || !strings.Contains(env, "KAFKA_CA_FILE") {
			t.Errorf("%s does not use %s with KAFKA_CA_FILE: %s", d, kafkaTLS, env)
		}
	}
}

// AC-6 runs once per deploy of the listener switch, not in the regular suite:
// start it with E2E_LISTENER_SWITCH=1, then let ArgoCD sync the step (or run
// prototype/kafka-ca.sh). A pod next to the API Gateway sends usage events to
// UsageWriteAPI until Kafka and the clients have rolled and settled; then no
// acknowledged event may be missing and fewer than 1% of requests may have
// failed (QS-17). E2E_SWITCH_POD=<pod> adopts a sender pod that is already
// running, for a run whose watcher stopped early: the pod's log covers the
// whole step.
//
// Verifies: kafka-client-auth AC-6; QS-17
func TestListenerSwitchKeepsAcknowledgedEvents(t *testing.T) {
	if os.Getenv("E2E_LISTENER_SWITCH") == "" {
		t.Skip("set E2E_LISTENER_SWITCH=1 and sync a step of the listener switch while it runs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	name := os.Getenv("E2E_SWITCH_POD")
	adopted := name != ""
	if !adopted {
		name = "e2e-switch-" + randomID()
		startSender(ctx, t, name)
	}
	defer func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer dcancel()
		_, _ = retryKubectl(dctx, "-n", "gateway", "delete", "pod", name, "--wait=false")
	}()

	// A step counts as changed when a generation moves, or when the test
	// starts while something is still rolling.
	start, _ := rolloutState(ctx)
	changed := adopted || !settled(ctx)
	t.Logf("sending events from %s; sync the step now (start: %s)", name, start)
	settledFor := 0
	for ctx.Err() == nil {
		time.Sleep(10 * time.Second)
		if now, err := rolloutState(ctx); err == nil && now != start {
			changed = true
		}
		if changed && settled(ctx) {
			settledFor++
		} else {
			settledFor = 0
		}
		// Keep sending for a minute after everything is Ready.
		if settledFor >= 6 {
			break
		}
	}
	if !changed {
		t.Fatal("nothing rolled within the time limit; was the step synced?")
	}
	if _, err := retryKubectl(ctx, "-n", "gateway", "exec", name, "--", "touch", "/tmp/stop"); err != nil {
		t.Fatal(err)
	}
	var log string
	for ctx.Err() == nil {
		time.Sleep(3 * time.Second)
		out, err := kubectl(ctx, "-n", "gateway", "logs", name)
		if err == nil && strings.Contains(out, "\ndone") {
			log = out
			break
		}
	}
	var acked []string
	failed := 0
	for _, line := range strings.Split(log, "\n") {
		id, code, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if code == "202" {
			acked = append(acked, id)
		} else {
			failed++
		}
	}
	total := len(acked) + failed
	rate := 100 * float64(failed) / float64(max(1, total))
	t.Logf("%d acknowledged, %d failed (%.2f%%) during the step", len(acked), failed, rate)
	if len(acked) == 0 {
		t.Fatal("no event was acknowledged")
	}
	if rate >= 1 {
		t.Errorf("%.2f%% of requests failed, want below 1%%", rate)
	}
	waitForStoredIDs(t, acked, 5*time.Minute)
}

// startSender starts the pod that sends one usage event of tenant A every
// 200 ms and logs "<event_id> <status>" per request until /tmp/stop exists.
// Event IDs are UUID-shaped: <prefix>-0000-4000-8000-<counter>.
func startSender(ctx context.Context, t *testing.T, name string) {
	t.Helper()
	script := fmt.Sprintf(`i=0
while [ ! -f /tmp/stop ]; do
  i=$((i+1))
  id=$(printf '%s-0000-4000-8000-%%012d' $i)
  now=$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)
  code=$(curl -s -m 15 -o /dev/null -w '%%{http_code}' -H 'Content-Type: application/json' \
    --data "{\"events\":[{\"event_id\":\"$id\",\"event_time\":\"$now\",\"tenant_id\":\"%s\",\"metric\":\"api_calls\",\"quantity\":1,\"attributes\":{\"path\":\"/e2e/listener-switch\"}}]}" \
    http://usage-writeapi.usage.svc:8080/v1/events)
  echo "$id $code"
  sleep 0.2
done
echo done`, randomID()[:8], tenA.OrgID)
	if _, err := retryKubectl(ctx, "-n", "gateway", "run", name, "--restart=Never", "--image=curlimages/curl:8.11.1",
		"--labels=app.kubernetes.io/name=api-gateway", "--command", "--", "sh", "-c", script); err != nil {
		t.Fatal(err)
	}
}

// retryKubectl retries kubectl while the API server is briefly unreachable,
// which happens on kind while every broker and client rolls.
func retryKubectl(ctx context.Context, args ...string) (string, error) {
	var out string
	var err error
	for range 5 {
		if out, err = kubectl(ctx, args...); err == nil || ctx.Err() != nil {
			return out, err
		}
		time.Sleep(3 * time.Second)
	}
	return out, err
}

// rolloutState is the generation of the Kafka cluster and of every Kafka
// client Deployment; it changes when a step of the switch is synced.
func rolloutState(ctx context.Context) (string, error) {
	var parts []string
	for _, r := range []string{"kafka/kafka/usage-kafka", "usage/deploy/usage-writeapi", "usage/deploy/usage-writer",
		"gateway/deploy/api-gateway", "tenants/deploy/tenants"} {
		f := strings.SplitN(r, "/", 3)
		g, err := retryKubectl(ctx, "-n", f[0], "get", f[1], f[2], "-o", "jsonpath={.metadata.generation}")
		if err != nil {
			return "", err
		}
		parts = append(parts, f[2]+"="+g)
	}
	return strings.Join(parts, " "), nil
}

// settled reports whether the Kafka cluster is Ready for its latest
// generation, every broker pod is Ready and every client Deployment is rolled.
func settled(ctx context.Context) bool {
	k, err := kubectl(ctx, "-n", "kafka", "get", "kafka", "usage-kafka", "-o",
		`jsonpath={.metadata.generation} {.status.observedGeneration} {.status.conditions[?(@.type=="Ready")].status}`)
	if err != nil {
		return false
	}
	f := strings.Fields(k)
	if len(f) != 3 || f[0] != f[1] || f[2] != "True" {
		return false
	}
	pods, err := kubectl(ctx, "-n", "kafka", "get", "pods", "-l", "strimzi.io/cluster=usage-kafka", "-o",
		`jsonpath={range .items[*]}{.status.conditions[?(@.type=="Ready")].status} {end}`)
	if err != nil || strings.Contains(pods, "False") || strings.TrimSpace(pods) == "" {
		return false
	}
	for _, d := range []string{"usage/usage-writeapi", "usage/usage-writer", "gateway/api-gateway", "tenants/tenants"} {
		ns, name, _ := strings.Cut(d, "/")
		s, err := kubectl(ctx, "-n", ns, "get", "deploy", name, "-o",
			"jsonpath={.metadata.generation} {.status.observedGeneration} {.spec.replicas} {.status.updatedReplicas} {.status.availableReplicas} {.status.replicas}")
		if err != nil {
			return false
		}
		v := strings.Fields(s)
		if len(v) != 6 || v[0] != v[1] {
			return false
		}
		want, _ := strconv.Atoi(v[2])
		for _, x := range v[3:] {
			if n, _ := strconv.Atoi(x); n != want {
				return false
			}
		}
	}
	return true
}
