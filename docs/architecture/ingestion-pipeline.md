# Ingestion & Telemetry Pipeline

[Template: Populate with the usage event pipeline (UsageWriteAPI, Kafka, Usage Writer, KEDA), burst handling and load leveling. See ADR-0001, item 4.]

Kafka runs on the Strimzi operator inside EKS and on the local kind cluster: [ADR-0005](../adr/0005-kafka-on-strimzi.md) (durability settings, node pools, scaling).

The usage store is the main PostgreSQL cluster in v1, behind a store interface, and hot, warm and cold tiers in v2: [ADR-0006](../adr/0006-usage-store.md). Duplicates are dropped by a unique key in that store: [ADR-0007](../adr/0007-usage-event-deduplication.md).
