# ADR-0008: The usage store runs on its own CNPG cluster with TimescaleDB

Date: 2026-10-25

## Status

Accepted

Decided in HAMSP-25. Amends [ADR-0006](0006-usage-store.md): the usage database leaves the main cluster, and the hot and warm tiers are one TimescaleDB hypertable. Amends [ADR-0002](0002-data-isolation-strategy.md) for the usage database: isolation there is enforced by a write function and views, not by RLS. Keeps [ADR-0007](0007-usage-event-deduplication.md).

## Context

ADR-0006 put usage events into the main CNPG cluster for v1 and named its risks: usage writes share synchronous replication and backups with OLTP, and 60–70 B rows over 13 months (Sizing) do not fit there. It planned hot, warm and cold tiers for v2, with TimescaleDB as a warm tier candidate (HAMSP-25).

What usage needs from its store:

* exact deduplication by `(tenant_id, event_id, event_time)` (ADR-0007), also for an event that arrives days late;
* per-tenant sums of a metric over a time range, for quotas, billing periods and reports;
* raw events for 13 months (billing disputes), until a cold tier in S3 exists;
* deleting one tenant's events (QS-18);
* tenant isolation enforced by the database, not only by the services (ADR-0002).

Facts that shaped the decision, checked on `timescale/timescaledb:2.27.2-pg18`:

* Columnstore compression, continuous aggregates, retention policies and jobs are under the Timescale License (TSL). The Apache 2 edition has none of them, so it would add nothing to plain day partitions.
* TimescaleDB refuses both on a table with row-level security: `columnstore cannot be used on table with row security` and `cannot create continuous aggregate on hypertable with row security`. RLS flags are not copied to chunks either.
* The unique key is enforced in compressed chunks: `ON CONFLICT DO NOTHING` drops a duplicate whose original is already compressed.
* CNPG 1.23, which the prototype ran, is out of support. CNPG 1.30 loads extensions from container images (ImageVolume, PostgreSQL 18, Kubernetes 1.35). The official `timescaledb-oss` extension image is the Apache 2 edition only. TimescaleDB loads its TSL module as `$libdir/timescaledb-tsl-<version>`, and PostgreSQL 18 searches `dynamic_library_path` for such names, so a TSL build works from a mounted image.

## Decision

### 1. Own cluster

The usage database runs on its own CNPG cluster, `usage-pg-cluster` in namespace `usage-db`, built like the main one (ADR-0001 items 5 and 6): 3 instances, quorum synchronous replication (ANY 1), a PgBouncer pooler in transaction mode, Barman backups to S3. It runs PostgreSQL 18. The main cluster keeps OLTP and Zitadel and no longer has a `usage` database or usage roles.

The CNPG operator moves from 1.23 to 1.30 for both clusters.

### 2. TimescaleDB under the TSL, from our extension image

The cluster loads TimescaleDB through ImageVolume (`spec.postgresql.extensions`) and `shared_preload_libraries`. The extension image is built from `images/timescaledb/Dockerfile`: the TSL build of TimescaleDB for PostgreSQL 18 on Debian trixie, for `linux/amd64` and `linux/arm64` (C-06). The PostgreSQL image stays the official CNPG one. CNPG's `Database` resource creates the database and the extension.

Upgrading TimescaleDB means a new extension image (it may carry the previous version's library, so replicas keep working between the restart and the update) and the `version` of the extension in the `Database` resource, which runs `ALTER EXTENSION ... UPDATE`.

### 3. Tiers inside one hypertable

| Tier | What | How |
|---|---|---|
| **Hot** | The last 8 days: Kafka topic retention (7 days, ADR-0005) + 1 day. Every redelivery from Kafka lands here. | Uncompressed 1-day chunks of `usage_events`. |
| **Warm** | Older than 8 days, up to 13 months. | The same chunks, converted to columnstore by a policy: `segmentby tenant_id`, `orderby event_time, event_id`. |
| **Aggregate** | Hourly sums per tenant and metric, 13 months. | Continuous aggregate `usage_hourly` (`sum(quantity)`, `count(*)`), refreshed by a policy. |
| **Cold** | Older data in S3. | v2, not decided here. |

Retention policies drop raw chunks and aggregate rows older than 13 months. Billing and quotas read the aggregate; a closed period can be checked against the compressed raw events, which a column scan reads quickly.

### 4. Isolation without RLS in the usage database

RLS cannot coexist with compression and aggregates, so the usage database enforces tenant isolation differently. The database still enforces it; no service gets more than its function or view lets it do.

1. Application roles (`usage_writer`, `usage_reader`) have **no privileges on any table**: not on `usage_events`, its chunks, the aggregate or its materialization table.
2. **Writes** go only through `write_usage_events(...)`, a `SECURITY DEFINER` function owned by `usage_owner`. It rejects the whole batch if no tenant is set in `app.current_tenant_id` or any event has another `tenant_id`, which is what an RLS `WITH CHECK` policy did. It inserts with `ON CONFLICT DO NOTHING` and returns the new events (ADR-0007).
3. **Reads** go only through `security_barrier` views filtered by `app.current_tenant_id` (`usage_events_tenant`, `usage_hourly_tenant`), which is what an RLS `USING` policy did. Without a tenant they return nothing.
4. The CI check changes its rule for this database: a table with `tenant_id` must have forced RLS **or** no privileges granted to anyone but its owner. ExampleApp and every other database keep the strict rule of ADR-0002.

### 5. Security guardrail: the usage cluster is platform-only

Only platform services (Usage Writer, later Metering Query Service) connect to the usage cluster. No tenant gets a connection, a role or a query interface to it, so the function and the views guard against bugs in our services, not against tenants writing SQL. If tenants get richer access to their usage in v2, it goes through derived data (for example TimescaleDB jobs or an export to a tenant-facing store), never through direct access to this cluster.

## Considered options

| Option | Why not |
|---|---|
| Keep usage in the main cluster (ADR-0006 v1) | The risks of ADR-0006 come true at Sizing: shared WAL and synchronous replication with OLTP, backups and restore growing with usage data. |
| Hot in the main cluster, warm in a TimescaleDB cluster | Needs a mover job between clusters, a partition catalog and read routing, and deduplication across two stores. One hypertable gives the same tiers with none of that. |
| Plain PostgreSQL partitions with RLS on the new cluster | Keeps ADR-0002 as it is, but there is no compression: 13 months of raw events are about 10 TB per replica at Sizing, so the cold tier would be needed at once. |
| TimescaleDB with RLS | Not possible: TimescaleDB refuses columnstore and continuous aggregates on tables with RLS. |
| An own hourly table with forced RLS, filled by a job | Keeps RLS on the aggregate, but the job needs to bypass RLS and misses late changes outside its window. Analysed in HAMSP-28. |
| TimescaleDB Apache 2 edition (official `timescaledb-oss` image) | No compression, aggregates, retention policies or jobs. |
| Own PostgreSQL image with TimescaleDB on CNPG 1.23 | Works without an operator upgrade, but we would stay on an unsupported operator and maintain the PostgreSQL image ourselves. |
| ClickHouse | A new stateful system, deduplication only eventually (ADR-0007), row policies only on reads. |

## Consequences

**Good:**

* Usage load, WAL and backups leave the main cluster; OLTP and Zitadel no longer share them.
* Deduplication stays exact for the whole 13 months, compressed chunks included, so the temporary limit of ADR-0007 does not apply to the warm tier.
* Compression and the aggregate come from TimescaleDB policies; our code only writes and reads.
* Deleting one tenant's events works in compressed chunks too (`segmentby tenant_id`).
* The PostgreSQL image is the official one; we build only the small extension image.

**Bad / risks:**

* **Isolation in the usage database depends on one function and two views**, not on RLS. Their tests (HAMSP-25 AC-5, AC-10) carry the weight that RLS policies carry elsewhere, and the CI check must keep every table closed to application roles.
* **TSL licence.** Using TimescaleDB inside our SaaS is allowed; offering it as a database service is not. Users of this MIT boilerplate inherit the same terms for the usage cluster. Whether our extension image may be pushed to a public registry is open (HAMSP-25).
* **Our extension image** must be rebuilt for every TimescaleDB and PostgreSQL major version and kept on the same Debian release as the CNPG image.
* **One more 3-instance cluster** to run and pay for. The cost against C-02 cannot be checked while C-02 is `TODO`.
* **ImageVolume** needs Kubernetes 1.35 and containerd 2.1+ on every node, also in EKS.
* The in-tree Barman backup is deprecated in CNPG and removed in 1.31; both clusters must move to the Barman Cloud Plugin first (HAMSP-29).
* The aggregate's refresh policy and the columnstore policy run as background jobs on the primary. Their cost next to the writes is measured below.

## Measurements

`prototype/bench/usage-tiers.sh` on the kind usage cluster (3 instances, synchronous replication, laptop disk), 2026-10-25: 10 M events over 10 days, 100 tenants. 80% are `api_calls` with the attributes the API Gateway sends, including a random UUID in the path, which compresses badly; the rest are token and egress events without attributes.

| What | Result |
|---|---|
| Load, through synchronous replication | 67 000 events/s |
| Size before compression, heap + indexes | 397 bytes per event |
| Columnstore policy, ~1.5 days of chunks | 11.4 s, 563 MB → 96 MB: **ratio 5.8** |
| Late duplicate, original in a compressed chunk (p50 / p95) | 0.76 / 4.4 ms, not stored |
| Late duplicate, original in an uncompressed chunk (p50 / p95) | 0.73 / 4.3 ms, not stored |
| New late event into a compressed chunk (p50 / p95) | 0.11 / 1.8 ms |
| Aggregate: first refresh over all data | 9.2 s, 71 700 rows, 9 MB |
| Aggregate: policy refresh after 500 late events | 0.1 s |
| One tenant's sum over 10 days: raw events / aggregate | 7 250 ms / 5 ms, same result |

What follows from it:

* A late duplicate costs the same whether its original is compressed or not, so the 8-day hot window is about cheap writes and corrections, not about deduplication.
* Billing and quotas must read the aggregate: three orders of magnitude faster than summing raw events.
* At Sizing (170 M events a day) this is about 67 GB a day uncompressed and 12 GB a day compressed, per instance: about 0.5 TB for the hot 8 days and 4.5 TB for the rest of 13 months, times 3 instances. Real attributes may compress better than the random UUIDs here, but the cold tier in S3 (v2) is what brings the cluster down to a size that restores within QS-13; raw events older than a few months should move there first.
