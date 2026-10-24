# Roadmap

What belongs to which version. Each line points to the decision or issue that holds the details; status lives in Linear (team `HAMSP`), not here.

## v1: platform skeleton (HAMSP-23)

The thinnest working version of every core service, merged to `master` once.

* Request path with tenant isolation: Cilium Gateway API → API Gateway → ExampleApp → PostgreSQL with RLS (HAMSP-5, [ADR-0002](adr/0002-data-isolation-strategy.md), [ADR-0004](adr/0004-api-gateway-go-service.md)).
* Tenant Management (FR-01): after the tenant lifecycle decision (HAMSP-6).
* Usage pipeline (FR-05): UsageWriteAPI → Kafka on Strimzi ([ADR-0005](adr/0005-kafka-on-strimzi.md)) → Usage Writer → usage store in the main PostgreSQL behind a store interface ([ADR-0006](adr/0006-usage-store.md)).
* Metering Query Service (FR-05): reads through the same store interface; its read API follows HAMSP-9.
* Per-tenant rate limits and quotas in the API Gateway (FR-07): after HAMSP-15.

## v2

* **Usage store by temperature:** hot, warm and cold tiers, cold data in S3 ([ADR-0006](adr/0006-usage-store.md)). Warm tier candidates: TimescaleDB (HAMSP-25), ClickHouse.
* **Billing** (FR-06) and the payment provider.
