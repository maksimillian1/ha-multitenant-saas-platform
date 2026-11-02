# ADR-0006: Usage store: the main PostgreSQL in v1, temperature tiers in v2

Date: 2026-10-24

## Status

Accepted

Decided in HAMSP-14. Amends [ADR-0001](0001-system-boundaries.md), item 5: in v1 the main PostgreSQL cluster also holds the usage events.

Amended by [ADR-0008](0008-usage-store-on-timescaledb.md): the usage database runs on its own CNPG cluster with TimescaleDB, and the hot and warm tiers are one hypertable (uncompressed and columnstore chunks) instead of partitions moved between stores. The store interface and the cold tier in S3 (v2) stay as decided here.

## Context

Usage Writer stores usage events per tenant, and Metering Query Service reads them back (FR-05, ADR-0001 item 4). At Sizing this is about 170 M events a day and 60–70 B rows over the 13-month retention.

Three things are not settled yet:

* how usage will be read, per-tenant counters only or ad-hoc analytics too (HAMSP-9);
* which store fits that best: ClickHouse, Timescale, S3 with a query engine, or a mix;
* the real event size and load.

The usage pipeline skeleton (HAMSP-23) needs a store now, and we do not want a new stateful system before these answers exist.

## Decision

### v1: the main PostgreSQL cluster, behind a store interface

1. **Where:** usage events go to the main CNPG cluster, in their own database `usage` with their own roles. Nothing else lives in that database.
2. **Layout:** one table partitioned by day on `event_time` (PostgreSQL declarative partitioning). A scheduled job creates partitions ahead and drops expired ones. A day partition is the unit that later moves between tiers.
3. **Isolation:** the table has forced RLS like every multi-tenant table (ADR-0002). Usage Writer sets `app.current_tenant_id` for each tenant's group of events inside the batch transaction.
4. **Store interface:** only Usage Writer (write side) and Metering Query Service (read side) touch usage tables, and only through a Go interface that speaks in events and per-tenant usage, never SQL:
   * **write** a batch of events and get back the events that were new (deduplication and quota counters, [ADR-0007](0007-usage-event-deduplication.md));
   * **read** the usage of one tenant for a metric and a time range at a granularity. The exact read API follows HAMSP-9.

   Changing the store means a new implementation of this interface. No other service changes.

### v2: data split by temperature

The store keeps every day partition in one of these tiers, and moves partitions down by age, so that most data sits in the cheapest place:

| Tier | What | Where (direction, not decided) |
|---|---|---|
| **Hot** | Recent days: written, deduplicated, read often (current billing period, quota rebuild). | PostgreSQL, as in v1. |
| **Warm** | Closed periods, still read for reports. Compressed or aggregated. | Candidates: TimescaleDB (HAMSP-25), ClickHouse, plain partitions with aggregate tables. |
| **Cold** | Older data, read rarely (audits, billing disputes, recalculation). | S3, probably Parquet, queried on demand (Athena or DuckDB). |

* A catalog records the tier and location of every partition.
* The read side of the interface routes a query by its time range to the tiers it covers.
* A scheduled job moves partitions to the next tier and verifies row counts before it drops the source.

Tier windows, formats and engines are decided in the v2 storage ADR.

## Considered options

| Option | Why not (for now) |
|---|---|
| ClickHouse | A new stateful system for one person to run, chosen before we know how usage is read (HAMSP-9). Stays a candidate for the warm tier. |
| TimescaleDB now | Needs our own CNPG image, its compression and aggregates are under the TSL licence, and RLS does not apply to its continuous aggregates. Analysed for the warm tier in HAMSP-25. |
| A separate CNPG cluster for usage | Keeps usage load away from OLTP, but it is one more cluster to run and pay for. The store interface makes this move cheap if the risks below show up. |
| S3 with Athena only | Freshness in minutes, query latency in seconds, small files, no uniqueness. Becomes the cold tier. |

## Consequences

**Good:**

* No new technology; the usage pipeline skeleton can start now.
* PostgreSQL enforces uniqueness, so deduplication is exact (ADR-0007).
* Dropping a day partition is instant and leaves no bloat.
* The store can change behind the interface without touching other services.

**Bad / risks:**

* **Usage writes share the main cluster with OLTP** (ExampleApp, Zitadel's database). Every event goes through synchronous replication, so WAL volume grows and tenant requests can see higher commit latency. This is a noisy neighbour between workloads, which QS-07 and QS-08 tests must watch.
* **Backups and restore grow with usage data.** The QS-13 restore within 30 minutes is at risk.
* **v1 does not meet Sizing at 13-month retention:** 60–70 B rows do not belong in the main OLTP cluster. v1 serves the skeleton and early load; the v2 tiers must land before production load.
* Revisit earlier, by moving the usage database to its own cluster behind the same interface, if:
  * the `usage` database takes more than half of the cluster's storage;
  * a restore drill exceeds 30 minutes (QS-13);
  * OLTP p95 grows under usage load.
* Deleting a tenant's usage events within 24 hours (QS-18) against the 13-month retention is still open (HAMSP-20).
