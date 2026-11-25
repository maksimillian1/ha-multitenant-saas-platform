package e2e

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

const appViaPooler = "host=main-pg-cluster-pooler-rw port=5432 dbname=exampleapp user=exampleapp password=dev-exampleapp sslmode=require"

// longQuery runs sql as ExampleApp's role through the pooler and returns how long it ran and psql's output.
func longQuery(t *testing.T, sql string) (time.Duration, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	primary := kube(t, time.Minute, "-n", "database", "get", "cluster", "main-pg-cluster", "-o", "jsonpath={.status.currentPrimary}")
	start := time.Now()
	out, err := kubectl(ctx, "-n", "database", "exec", primary, "-c", "postgres", "--",
		"psql", appViaPooler, "-Atq", "-v", "ON_ERROR_STOP=1", "-c", sql)
	took := time.Since(start)
	if err != nil {
		out += err.Error()
	}
	return took, out, err
}

func p95(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[(len(s)*95+99)/100-1]
}

func listLatencies(t *testing.T, tok string, n int, every time.Duration, stop <-chan struct{}) []time.Duration {
	t.Helper()
	var out []time.Duration
	for i := 0; n == 0 || i < n; i++ {
		select {
		case <-stop:
			return out
		default:
		}
		start := time.Now()
		r, err := doCall(http.MethodGet, "/items", tok, nil)
		out = append(out, time.Since(start))
		if err != nil {
			t.Errorf("tenant B GET /items: %v", err)
		} else if r.Status != http.StatusOK {
			t.Errorf("tenant B GET /items: status %d, want 200; body %.200s", r.Status, r.Body)
		}
		time.Sleep(every)
	}
	return out
}

// Verifies: main-database AC-1, AC-2, AC-3; T-01, T-12; QS-12
func TestTenantQueryCutAfter30s(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a 30-second query")
	}
	tokB := token(t, tenB)
	createItem(t, tokB, "query-timeout-b-"+randomID())
	before := listLatencies(t, tokB, 20, 200*time.Millisecond, nil)

	marker := "qs12-" + randomID()
	stop := make(chan struct{})
	during := make(chan []time.Duration)
	go func() { during <- listLatencies(t, tokB, 0, 500*time.Millisecond, stop) }()
	took, out, err := longQuery(t, "BEGIN; SET LOCAL statement_timeout = 0; SELECT set_config('app.current_tenant_id', '"+tenA.OrgID+"', true); SELECT pg_sleep(60) /* "+marker+" */; COMMIT;")
	cut := time.Now()
	close(stop)
	latencies := <-during

	if err == nil || !strings.Contains(out, "query timeout") {
		t.Fatalf("tenant A's 60 s query: err %v, output %q; want PgBouncer's query timeout", err, out)
	}
	if took < 29*time.Second || took > 34*time.Second {
		t.Errorf("tenant A's query was cut after %s, want about 30 s", took.Round(100*time.Millisecond))
	}
	if limit := 2*p95(before) + 200*time.Millisecond; p95(latencies) > limit {
		t.Errorf("tenant B's p95 during tenant A's query: %s over %d requests, want at most %s (before: %s)",
			p95(latencies), len(latencies), limit, p95(before))
	}
	for {
		n, err := psql(t, "SELECT count(*) FROM pg_stat_activity WHERE query LIKE '%"+marker+" */%' AND pid <> pg_backend_pid()")
		if err != nil {
			t.Fatal(err)
		}
		if n == "0" {
			break
		}
		if time.Since(cut) > 15*time.Second {
			t.Fatalf("PostgreSQL still runs tenant A's query %s after PgBouncer cut it", time.Since(cut).Round(time.Second))
		}
		time.Sleep(time.Second)
	}
}

// Verifies: main-database AC-4
func TestDirectConnectionsHaveNoQueryLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a 32-second query")
	}
	start := time.Now()
	if out, err := psql(t, "SELECT pg_sleep(32)"); err != nil {
		t.Fatalf("a 32 s query of an admin on the primary failed after %s: %v: %s", time.Since(start).Round(time.Second), err, out)
	}
	settings, err := psql(t, "SELECT current_setting('statement_timeout') || ' ' || current_setting('client_connection_check_interval')")
	if err != nil {
		t.Fatal(err)
	}
	if settings != "0 10s" {
		t.Errorf("statement_timeout and client_connection_check_interval: %q, want \"0 10s\"", settings)
	}
	for _, s := range []struct{ ns, secret string }{{"exampleapp", "exampleapp-db"}, {"tenants", "tenants-db"}} {
		raw, err := base64.StdEncoding.DecodeString(kube(t, time.Minute, "-n", s.ns, "get", "secret", s.secret, "-o", "jsonpath={.data.MIGRATE_DATABASE_URL}"))
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(string(raw))
		if err != nil {
			t.Fatalf("%s/%s: MIGRATE_DATABASE_URL: %v", s.ns, s.secret, err)
		}
		if u.Hostname() != "main-pg-cluster-rw.database.svc" {
			t.Errorf("%s/%s: migrations connect to %s, want the primary main-pg-cluster-rw.database.svc", s.ns, s.secret, u.Hostname())
		}
	}
}
