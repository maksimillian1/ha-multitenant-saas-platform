# Ingestion & Telemetry Pipeline

[Template: Populate with the usage event pipeline (UsageWriteAPI, Kafka, Usage Writer, KEDA), burst handling and load leveling. See ADR-0001, item 4.]

Kafka runs on the Strimzi operator inside EKS and on the local kind cluster: [ADR-0005](../adr/0005-kafka-on-strimzi.md) (durability settings, node pools, scaling).

The usage store sits behind a store interface ([ADR-0006](../adr/0006-usage-store.md)) and runs on its own CNPG cluster with TimescaleDB: hot and warm tiers in one hypertable, an hourly aggregate, 13 months of raw events ([ADR-0008](../adr/0008-usage-store-on-timescaledb.md)). The cold tier in S3 is v2. Duplicates are dropped by a unique key in that store: [ADR-0007](../adr/0007-usage-event-deduplication.md).
