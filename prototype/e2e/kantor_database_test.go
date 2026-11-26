package e2e

import (
	"context"
	"strings"
	"testing"
	"time"
)

const kantorViaPooler = "host=main-pg-cluster-pooler-rw port=5432 dbname=kantor user=kantor_app password=dev-kantor-app sslmode=require"

// kantorApp runs sql as KANTOR's runtime role through the pooler, from the primary's pod.
func kantorApp(t *testing.T, timeout time.Duration, sql string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	primary := kube(t, time.Minute, "-n", "database", "get", "cluster", "main-pg-cluster", "-o", "jsonpath={.status.currentPrimary}")
	out, err := kubectlStdin(ctx, sql+"\n", "-n", "database", "exec", "-i", primary, "-c", "postgres", "--",
		"psql", kantorViaPooler, "-Atq", "-v", "ON_ERROR_STOP=1", "-f", "-")
	return strings.TrimSpace(out), err
}

// Verifies: main-database AC-5
func TestKantorRoles(t *testing.T) {
	out, err := psqlDB(t, "kantor", `
		SELECT concat_ws('|', pg_get_userbyid(d.datdba), r.rolsuper, r.rolbypassrls, r.rolcreaterole,
		                 r.rolcreatedb, r.rolinherit, pg_has_role('kantor_app', 'kantor_owner', 'MEMBER'))
		FROM pg_database d, pg_roles r
		WHERE d.datname = 'kantor' AND r.rolname = 'kantor_app'`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "kantor_owner|f|f|f|f|f|f"; out != want {
		t.Fatalf("owner|super|bypassrls|createrole|createdb|inherit|member of owner = %q, want %q", out, want)
	}
	owner, err := psqlDB(t, "kantor", `SELECT concat_ws('|', rolsuper, rolbypassrls, rolcreaterole) FROM pg_roles WHERE rolname = 'kantor_owner'`)
	if err != nil {
		t.Fatal(err)
	}
	if owner != "f|f|f" {
		t.Fatalf("kantor_owner super|bypassrls|createrole = %q, want f|f|f", owner)
	}
}

// Verifies: main-database AC-6; T-01, T-12
func TestKantorAppRoleCannotLeaveItsTenant(t *testing.T) {
	table := "e2e_rls_" + randomID()
	tenantA, tenantB := newUUID(), newUUID()
	setup := `SET ROLE kantor_owner;
		CREATE TABLE ` + table + ` (tenant_id uuid NOT NULL, v text NOT NULL);
		ALTER TABLE ` + table + ` ENABLE ROW LEVEL SECURITY;
		ALTER TABLE ` + table + ` FORCE ROW LEVEL SECURITY;
		CREATE POLICY tenant_isolation ON ` + table + ` USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
		GRANT SELECT, INSERT ON ` + table + ` TO kantor_app;
		RESET ROLE;
		INSERT INTO ` + table + ` VALUES ('` + tenantA + `', 'a'), ('` + tenantB + `', 'b');`
	if _, err := psqlDB(t, "kantor", setup); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = psqlDB(t, "kantor", "DROP TABLE IF EXISTS "+table) })

	asTenant := func(tenant, sql string) string {
		return "BEGIN; SELECT set_config('app.current_tenant', '" + tenant + "', true) \\g /dev/null\n" + sql + "; COMMIT;"
	}
	got, err := kantorApp(t, time.Minute, asTenant(tenantA, "SELECT string_agg(v, ',') FROM "+table))
	if err != nil {
		t.Fatal(err)
	}
	if got != "a" {
		t.Fatalf("tenant A sees %q, want only a", got)
	}
	if got, err := kantorApp(t, time.Minute, "SELECT count(*) FROM "+table); err != nil || got != "0" {
		t.Fatalf("no tenant: %q, %v; want 0 rows", got, err)
	}
	if _, err := kantorApp(t, time.Minute, asTenant(tenantA, "INSERT INTO "+table+" VALUES ('"+tenantB+"', 'forged')")); err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("insert for tenant B as tenant A: %v, want a row-level security violation", err)
	}

	for name, sql := range map[string]string{
		"disable RLS":       "ALTER TABLE " + table + " DISABLE ROW LEVEL SECURITY",
		"drop the policy":   "DROP POLICY tenant_isolation ON " + table,
		"row_security off":  "SET row_security = off; SELECT count(*) FROM " + table,
		"become the owner":  "SET ROLE kantor_owner",
		"grant bypassrls":   "ALTER ROLE kantor_app BYPASSRLS",
		"create a database": "CREATE DATABASE " + table,
	} {
		if out, err := kantorApp(t, time.Minute, sql); err == nil {
			t.Errorf("%s as kantor_app succeeded (%q), want an error", name, out)
		}
	}
	if got, err := psqlDB(t, "kantor", "SELECT concat_ws('|', relrowsecurity, relforcerowsecurity) FROM pg_class WHERE relname = '"+table+"'"); err != nil || got != "t|t" {
		t.Fatalf("after the attempts rowsecurity|force = %q, %v; want t|t", got, err)
	}
}

// Verifies: main-database AC-7
func TestPoolerKeepsPreparedStatements(t *testing.T) {
	pod := kube(t, time.Minute, "-n", "database", "get", "pods", "-l", "cnpg.io/poolerName=main-pg-cluster-pooler-rw",
		"-o", "jsonpath={.items[0].metadata.name}")
	ini := kube(t, time.Minute, "-n", "database", "exec", pod, "--", "cat", "/controller/configs/pgbouncer.ini")
	if !strings.Contains(ini, "max_prepared_statements = 200") {
		t.Fatalf("pgbouncer.ini has no max_prepared_statements = 200:\n%s", ini)
	}
}

// Verifies: main-database AC-8; T-12
func TestPoolerClosesIdleTransaction(t *testing.T) {
	if testing.Short() {
		t.Skip("holds a transaction idle for 70 seconds")
	}
	out, err := kantorApp(t, 2*time.Minute, "BEGIN; SELECT 1 \\g /dev/null\n\\! sleep 70\nSELECT 'still open'; COMMIT;")
	if err == nil || strings.Contains(out, "still open") {
		t.Fatalf("transaction idle for 70 s: %q, %v; want the pooler to close it", out, err)
	}
}
