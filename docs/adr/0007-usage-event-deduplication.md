# ADR-0007: Usage event deduplication by a unique key in the usage store

Date: 2026-10-24

## Status

Accepted. Temporary: tied to the v1 usage store and revisited with the v2 storage ADR.

Kept by [ADR-0008](0008-usage-store-on-timescaledb.md): the unique key lives on in the TimescaleDB hypertable and is enforced in compressed chunks too, so deduplication stays exact for the whole 13 months. The limit below (a duplicate that arrives after its original left the hot tier) now applies only to a future cold tier in S3.

Decided in HAMSP-7. Details the duplicate handling of [ADR-0001](0001-system-boundaries.md), item 4.

## Context

A usage event must end up as one record even when it arrives several times (QS-06), and no acknowledged event may be lost (QS-09). Duplicates come from three places:

1. A client retries after a timeout: UsageWriteAPI already put the event into Kafka, but the response was lost, so Kafka holds two messages with the same event.
2. The producer retries a send to Kafka. The idempotent producer covers this ([ADR-0005](0005-kafka-on-strimzi.md)).
3. Usage Writer writes a batch and crashes before it commits the Kafka offset, then writes the batch again. Kafka is at-least-once, so this will happen.

In v1 the usage store is PostgreSQL ([ADR-0006](0006-usage-store.md)), which can enforce uniqueness in the same transaction that stores the event.

## Decision

1. **Identity set by the sender.** Every usage event carries `event_id` and `event_time`, set once by the sender and kept on every retry. UsageWriteAPI rejects events without them.
   * Senders of single events (API Gateway, agent workloads) use a UUIDv7.
   * Senders of periodic totals (egress) derive the ID from tenant, metric and period, so a resend has the same ID.
2. **Unique key in the store.** The usage table has the unique key `(tenant_id, event_id, event_time)`. `event_time` is part of it because a unique key on a partitioned table must include the partition column. Usage Writer inserts with `ON CONFLICT DO NOTHING RETURNING`; the returned rows are the new events.
3. **Order of commits.** Usage Writer commits the database transaction first and the Kafka offset after it. A batch replayed after a crash inserts nothing.
4. **Quota counters** (HAMSP-15) grow only by the new events, after the transaction commits. If Usage Writer crashes between the commit and the counter update, the counter stays too low. It is rebuilt from the usage store, which stays the source of truth (ADR-0001).
5. **Proof.** The spike planned in HAMSP-7 becomes an acceptance criterion of the usage pipeline skeleton (HAMSP-23): the same event sent 3 times, and a batch replayed after a consumer restart, each leave exactly one row (QS-06).

## Considered options

| Option | Why not |
|---|---|
| Separate dedup store (Redis "seen" IDs) | Marking the ID and storing the event are two writes that cannot be atomic. A crash between them loses the event (QS-09) or keeps a duplicate (QS-06). |
| Idempotency check in UsageWriteAPI before Kafka | The same gap: the key is marked, the Kafka write fails, the client's retry is dropped and the event is lost. |
| Kafka transactions (exactly-once) | They cover writes inside Kafka only, not client retries (source 1) or writes to the store (source 3). |
| Window of seen IDs in Usage Writer, rebuilt from Kafka after a restart | Works with any store, but is exact only within the window and adds state to Usage Writer. Not needed while the store enforces uniqueness; a candidate for v2. |
| Kafka Streams or Flink | Exact within a window, but a JVM component for one person to run. |
| Duplicates collapsed by the store later (ClickHouse `ReplacingMergeTree`) or at query time | Eventual: billing queries would need `FINAL` or `DISTINCT`, and quota counters cannot tell new events from duplicates. |

## Consequences

**Good:**

* Exact and atomic. It covers all three sources, with no time window and no extra component.
* The unique index is per day partition, so each index stays the size of one day.

**Bad / risks:**

* **The main PostgreSQL cluster does the dedup work:** an index lookup and an index write per event, on the cluster that also serves OLTP (see the risks in ADR-0006). This may not hold the target load; the QS-07 and QS-08 tests decide.
* A retry with a different `event_time` lands in another partition and is not caught. Senders must keep both fields; the UsageWriteAPI contract in `docs/contracts/` must say so when it is written.
* **Temporary:** once v2 moves partitions out of PostgreSQL into tiers that do not enforce uniqueness, a duplicate that arrives after its original left the hot tier is not caught. The v2 storage ADR must keep the hot tier longer than the longest redelivery (Kafka retention and client retry horizon), or switch to a window in Usage Writer.
