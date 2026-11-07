package detect

import (
	"reflect"
	"testing"
)

// Verifies: c4-from-code AC-7
func TestTables(t *testing.T) {
	for sql, want := range map[string][]TableUse{
		"INSERT INTO events (id) VALUES ($1)":                      {{"events", "writes"}},
		"SELECT count(*) FROM events JOIN tenants t ON true":       {{"events", "reads"}, {"tenants", "reads"}},
		"DELETE FROM events WHERE id = $1":                         {{"events", "writes"}},
		"UPDATE events SET x = 1 FROM tenants":                     {{"events", "writes"}, {"tenants", "reads"}},
		"CREATE TABLE IF NOT EXISTS schema_migrations (v int)":     {{"schema_migrations", "defines"}},
		"SELECT a FROM write_usage_events($1::uuid[])":             {{"write_usage_events()", "calls"}},
		"CREATE OR REPLACE FUNCTION write_usage_events() RETURNS":  {{"write_usage_events()", "defines"}},
		"SELECT x FROM pg_class JOIN unnest($1) u ON true":         nil,
		"SELECT set_config('app.current_tenant_id', $1, true)":     nil,
		"SELECT * FROM events e JOIN LATERAL (SELECT 1) l ON true": {{"events", "reads"}},
	} {
		got := Tables(sql)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got  %v\n want %v", sql, got, want)
		}
	}
	if got := Tables("SELECT 1 FROM audit_log", "audit_*"); got != nil {
		t.Errorf("ignore: got %v", got)
	}
}
