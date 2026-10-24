# Ingestion & Telemetry Pipeline

[Template: Populate with the usage event pipeline (UsageWriteAPI, Kafka, Usage Writer, KEDA), burst handling and load leveling. See ADR-0001, item 4.]

Kafka runs on the Strimzi operator inside EKS and on the local kind cluster: [ADR-0005](../adr/0005-kafka-on-strimzi.md) (durability settings, node pools, scaling).
