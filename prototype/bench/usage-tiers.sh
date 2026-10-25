#!/bin/bash
# HAMSP-25 AC-12: measures the usage tiers on the kind usage cluster. Creates
# the scratch database usage_bench next to usage, applies the usage
# migrations, runs usage_tiers.sql and drops the database again (KEEP=1 keeps
# it). The numbers go into ADR-0008.
#
# EVENTS (default 10000000), TENANTS (100) and DAYS (10) size the data.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
NS=usage-db
CLUSTER=usage-pg-cluster
DB=usage_bench

PRIMARY=$(kubectl -n "${NS}" get cluster "${CLUSTER}" -o jsonpath='{.status.currentPrimary}')
psql() { kubectl -n "${NS}" exec -i "${PRIMARY}" -c postgres -- psql -U postgres -v ON_ERROR_STOP=1 "$@"; }

psql -d postgres -q -c "DROP DATABASE IF EXISTS ${DB} WITH (FORCE)" -c "CREATE DATABASE ${DB}"
[[ "${KEEP:-0}" == "1" ]] || trap 'psql -d postgres -q -c "DROP DATABASE IF EXISTS ${DB} WITH (FORCE)"' EXIT
psql -d "${DB}" -q -c "CREATE EXTENSION timescaledb"
cat "${REPO_DIR}"/apps/usage/internal/store/migrations/*.sql | psql -d "${DB}" -q >/dev/null

echo "Primary ${PRIMARY}: ${EVENTS:-10000000} events, ${TENANTS:-100} tenants, ${DAYS:-10} days"
psql -d "${DB}" -q -v events="${EVENTS:-10000000}" -v tenants="${TENANTS:-100}" -v days="${DAYS:-10}" \
  < "${SCRIPT_DIR}/usage_tiers.sql"
