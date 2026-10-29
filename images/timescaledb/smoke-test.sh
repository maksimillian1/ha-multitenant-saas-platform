#!/bin/bash
# Smoke test for the TimescaleDB extension image: starts the CNPG PostgreSQL
# image with the extension's files mounted the way CNPG's ImageVolume does
# (dynamic_library_path, extension_control_path), then checks that the TSL
# features work: licence, columnstore compression and a continuous aggregate.
#
# Usage: smoke-test.sh <extension image> [<postgresql image>]
set -euo pipefail

EXT_IMAGE="$1"
PG_IMAGE="${2:-ghcr.io/cloudnative-pg/postgresql:18.6-system-trixie}"
NAME="timescaledb-smoke-$$"
EXT_DIR="$(mktemp -d)"
trap 'docker rm -f "${NAME}" "${NAME}-ext" >/dev/null 2>&1 || true; rm -rf "${EXT_DIR}"' EXIT

docker create --name "${NAME}-ext" "${EXT_IMAGE}" /none >/dev/null
docker cp "${NAME}-ext:/lib" "${EXT_DIR}/lib"
docker cp "${NAME}-ext:/share" "${EXT_DIR}/share"
chmod -R a+rX "${EXT_DIR}"

# $libdir and $system keep the image's own libraries and extensions reachable,
# as CNPG does.
docker run -d --name "${NAME}" -v "${EXT_DIR}:/extensions/timescaledb:ro" "${PG_IMAGE}" bash -c '
  initdb -D /tmp/data -U postgres --auth=trust >/dev/null &&
  exec postgres -D /tmp/data -c listen_addresses= \
    -c shared_preload_libraries=timescaledb \
    -c timescaledb.telemetry_level=off \
    -c dynamic_library_path=/extensions/timescaledb/lib:\$libdir \
    -c extension_control_path=/extensions/timescaledb/share:\$system' >/dev/null

for _ in $(seq 1 30); do
  docker exec "${NAME}" pg_isready -q -U postgres 2>/dev/null && break
  sleep 1
done

docker exec -i "${NAME}" psql -U postgres -v ON_ERROR_STOP=1 -Atq <<'SQL'
CREATE EXTENSION timescaledb;
SELECT 'license=' || current_setting('timescaledb.license');
CREATE TABLE t (at timestamptz NOT NULL, tenant_id text NOT NULL, v bigint NOT NULL);
SELECT create_hypertable('t', by_range('at', interval '1 day')) IS NOT NULL;
ALTER TABLE t SET (timescaledb.enable_columnstore, timescaledb.segmentby = 'tenant_id', timescaledb.orderby = 'at');
INSERT INTO t SELECT now() - interval '3 days' + g * interval '1 second', 't' || (g % 10), 1 FROM generate_series(1, 10000) g;
SELECT 'compressed=' || count(compress_chunk(c)) FROM show_chunks('t', older_than => interval '2 days') c;
CREATE MATERIALIZED VIEW t_hourly WITH (timescaledb.continuous) AS
  SELECT tenant_id, time_bucket('1 hour', at) AS bucket, sum(v) AS v FROM t GROUP BY 1, 2 WITH NO DATA;
CALL refresh_continuous_aggregate('t_hourly', NULL, now());
SELECT 'aggregated=' || sum(v) FROM t_hourly;
SQL
