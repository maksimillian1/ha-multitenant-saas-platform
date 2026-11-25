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

// Verifies: tenant-query-timeout AC-1, AC-2; T-01
func TestTenantQueryCancelledAfter30s(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a 30-second query")
	}
	tokB := token(t, tenB)
	createItem(t, tokB, "query-timeout-b-"+randomID())
	before := listLatencies(t, tokB, 20, 200*time.Millisecond, nil)

	stop := make(chan struct{})
	during := make(chan []time.Duration)
	go func() { during <- listLatencies(t, tokB, 0, 500*time.Millisecond, stop) }()
	took, out, err := longQuery(t, "BEGIN; SELECT set_config('app.current_tenant_id', '"+tenA.OrgID+"', true); SELECT pg_sleep(60); COMMIT;")
	close(stop)
	latencies := <-during

	if err == nil || !strings.Contains(out, "canceling statement due to statement timeout") {
		t.Fatalf("tenant A's 60 s query: err %v, output %q; want a statement timeout", err, out)
	}
	if took < 29*time.Second || took > 34*time.Second {
		t.Errorf("tenant A's query was cancelled after %s, want about 30 s", took.Round(100*time.Millisecond))
	}
	if limit := 2*p95(before) + 200*time.Millisecond; p95(latencies) > limit {
		t.Errorf("tenant B's p95 during tenant A's query: %s over %d requests, want at most %s (before: %s)",
			p95(latencies), len(latencies), limit, p95(before))
	}
}

// Verifies: tenant-query-timeout AC-3; T-12
func TestAppCannotLiftQueryLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a 35-second query")
	}
	took, out, err := longQuery(t, "BEGIN; SET LOCAL statement_timeout = 0; SELECT pg_sleep(60); COMMIT;")
	if err == nil {
		t.Fatalf("a 60 s query with statement_timeout = 0 finished after %s: %q", took, out)
	}
	if took < 35*time.Second || took > 40*time.Second {
		t.Errorf("the query without a statement_timeout was cut after %s, want between 35 and 40 s (PgBouncer query_timeout); output %q",
			took.Round(100*time.Millisecond), out)
	}
}

// Verifies: tenant-query-timeout AC-4
func TestAppRolesHaveQueryLimit(t *testing.T) {
	settings, err := psql(t, `SELECT r.rolname || '=' || coalesce(array_to_string(s.setconfig, ','), '')
		FROM pg_roles r LEFT JOIN pg_db_role_setting s ON s.setrole = r.oid AND s.setdatabase = 0
		WHERE r.rolname IN ('exampleapp', 'exampleapp_owner', 'tenants_owner') ORDER BY r.rolname`)
	if err != nil {
		t.Fatal(err)
	}
	want := "exampleapp=statement_timeout=30s\nexampleapp_owner=\ntenants_owner="
	if settings != want {
		t.Errorf("role settings:\n%s\nwant:\n%s", settings, want)
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
