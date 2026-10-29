package e2e

// The usage cluster (HAMSP-25, ADR-0008): its own CNPG cluster on PostgreSQL 18
// with TimescaleDB from our extension image, and no usage data left in the
// main cluster.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// AC-2: the usage cluster runs 3 instances of PostgreSQL 18 with quorum
// synchronous replication, a transaction-mode pooler and a completed backup;
// TimescaleDB comes from the ImageVolume extension image with its TSL
// features, and its policy jobs are scheduled.
func TestUsageClusterRunsTimescaleDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	get := func(kind, name, path string) string {
		t.Helper()
		out, err := kubectl(ctx, "-n", "usage-db", "get", kind, name, "-o", "jsonpath="+path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	if got := get("cluster", "usage-pg-cluster", "{.status.readyInstances}/{.spec.instances}"); got != "3/3" {
		t.Errorf("ready/instances = %s, want 3/3", got)
	}
	if got := get("pooler", "usage-pg-cluster-pooler-rw", "{.spec.pgbouncer.poolMode}"); got != "transaction" {
		t.Errorf("pooler mode = %q, want transaction", got)
	}
	if got := get("backup", "usage-pg-cluster-manual-backup", "{.status.phase}"); got != "completed" {
		t.Errorf("backup phase = %q, want completed", got)
	}
	primary := get("cluster", "usage-pg-cluster", "{.status.currentPrimary}")
	if got := get("pod", primary, "{.spec.volumes[?(@.image)].image.reference}"); got != "hamsp/timescaledb:2.27.2-pg18" {
		t.Errorf("image volume of %s = %q, want hamsp/timescaledb:2.27.2-pg18", primary, got)
	}

	out, err := psqlUsage(t, `
		SELECT concat_ws('|', current_setting('server_version_num')::int / 10000,
		                 current_setting('synchronous_standby_names') LIKE 'ANY 1 (%',
		                 (SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'),
		                 current_setting('timescaledb.license'),
		                 (SELECT string_agg(proc_name, ',' ORDER BY proc_name) FROM timescaledb_information.jobs j
		                  LEFT JOIN timescaledb_information.continuous_aggregates c
		                         ON c.materialization_hypertable_name = j.hypertable_name
		                  WHERE coalesce(c.view_name, j.hypertable_name) IN ('usage_events', 'usage_hourly') AND j.scheduled))`)
	if err != nil {
		t.Fatal(err)
	}
	want := "18|t|2.27.2|timescale|policy_compression,policy_refresh_continuous_aggregate,policy_retention,policy_retention"
	if out != want {
		t.Fatalf("pg major|sync ANY 1|timescaledb|license|jobs = %q, want %q", out, want)
	}
}

// AC-4: the main cluster no longer has the usage database or the usage roles.
func TestMainClusterHasNoUsageDatabase(t *testing.T) {
	out, err := psqlDB(t, "postgres", `
		SELECT concat_ws('|',
		    (SELECT count(*) FROM pg_database WHERE datname = 'usage'),
		    (SELECT count(*) FROM pg_roles WHERE rolname IN ('usage_owner', 'usage_writer', 'usage_reader')))`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "0|0" {
		t.Fatalf("usage databases|usage roles on the main cluster = %q, want 0|0", out)
	}
}
