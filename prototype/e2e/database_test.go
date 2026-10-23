package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// AC-7: items has RLS enabled and forced; ExampleApp connects as a role that
// does not own it and is NOSUPERUSER NOBYPASSRLS.
func TestItemsTableRLSAndAppRole(t *testing.T) {
	out, err := psql(t, `
		SELECT concat_ws('|', c.relrowsecurity, c.relforcerowsecurity, pg_get_userbyid(c.relowner),
		                 r.rolsuper, r.rolbypassrls)
		FROM pg_class c, pg_roles r
		WHERE c.oid = 'public.items'::regclass AND r.rolname = 'exampleapp'`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "t|t|exampleapp_owner|f|f"; out != want {
		t.Fatalf("rowsecurity|forcerowsecurity|owner|rolsuper|rolbypassrls = %q, want %q", out, want)
	}

	// The connections ExampleApp opens (through PgBouncer) use that role.
	mustStatus(t, call(t, http.MethodGet, "/items", token(t, tenA), nil), http.StatusOK, "GET /items")
	users, err := psql(t, `
		SELECT string_agg(DISTINCT usename, ',') FROM pg_stat_activity
		WHERE datname = 'exampleapp' AND backend_type = 'client backend' AND pid <> pg_backend_pid()`)
	if err != nil {
		t.Fatal(err)
	}
	if users != "exampleapp" {
		t.Fatalf("roles connected to the exampleapp database = %q, want only exampleapp", users)
	}
}

// AC-8: without app.current_tenant_id the app role sees no rows and cannot insert.
func TestNoTenantContextFailsClosed(t *testing.T) {
	createItem(t, token(t, tenA), "ac8-"+randomID())

	n, err := psql(t, "SET ROLE exampleapp; SELECT count(*) FROM items")
	if err != nil {
		t.Fatal(err)
	}
	if n != "0" {
		t.Fatalf("SELECT without tenant context returned %s rows, want 0", n)
	}

	_, err = psql(t, "SET ROLE exampleapp; INSERT INTO items (tenant_id, name) VALUES ('"+tenA.OrgID+"', 'ac8-no-context')")
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("INSERT without tenant context: err = %v, want a row-level security violation", err)
	}
}
