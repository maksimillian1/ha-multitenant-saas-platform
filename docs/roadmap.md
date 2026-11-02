# Roadmap

What belongs to which version. Each line points to the decision or issue that holds the details; status lives in Linear (team `HAMSP`), not here.

## v1: platform skeleton (HAMSP-23)

The thinnest working version of every core service, merged to `master` once.

* Request path with tenant isolation: Cilium Gateway API → API Gateway → ExampleApp → PostgreSQL with RLS (HAMSP-5, [ADR-0002](adr/0002-data-isolation-strategy.md), [ADR-0004](adr/0004-api-gateway-go-service.md)).
* Tenant Management (FR-01): after the tenant lifecycle decision (HAMSP-6).
* Usage pipeline (FR-05): UsageWriteAPI → Kafka on Strimzi ([ADR-0005](adr/0005-kafka-on-strimzi.md)) → Usage Writer → usage store behind a store interface ([ADR-0006](adr/0006-usage-store.md)), duplicates dropped by a unique key ([ADR-0007](adr/0007-usage-event-deduplication.md)).
* Usage store on its own CNPG cluster with TimescaleDB: hot and warm tiers, hourly aggregate, CNPG 1.30 (HAMSP-25, [ADR-0008](adr/0008-usage-store-on-timescaledb.md)).
* Metering Query Service (FR-05): reads through the same store interface; its read API follows HAMSP-9.
* Per-tenant rate limits and quotas in the API Gateway (FR-07): after HAMSP-15.

## v2

* **Cold usage tier:** old chunks exported to S3, probably Parquet, queried on demand ([ADR-0006](adr/0006-usage-store.md)); hot and warm are in place since HAMSP-25 ([ADR-0008](adr/0008-usage-store-on-timescaledb.md)). Deduplication must be revisited for it ([ADR-0007](adr/0007-usage-event-deduplication.md)).
* **Tenant access to usage:** only through derived data (TimescaleDB jobs, an export to a tenant-facing store), never through the usage cluster (ADR-0008).
* **Billing** (FR-06) and the payment provider.
