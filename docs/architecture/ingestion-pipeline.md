# Ingestion & Telemetry Pipeline

[Template: Populate with the usage event pipeline (UsageWriteAPI, Kafka, Usage Writer, KEDA), burst handling and load leveling. See ADR-0001, item 4.]

Kafka runs on the Strimzi operator inside EKS and on the local kind cluster: [ADR-0005](../adr/0005-kafka-on-strimzi.md) (durability settings, node pools, scaling).

Every Kafka client (UsageWriteAPI, Usage Writer, Tenant Management, the API Gateway) connects over TLS as its own KafkaUser with SCRAM-SHA-512, and simple ACLs limit it to its topic and group: [kafka-client-auth](../features/kafka-client-auth.yaml) (HAMSP-31). The cluster CA is ours (`generateCertificateAuthority: false`); on kind its key stays out of Git (`prototype/kafka-ca.sh`), on EKS it is to come from External Secrets (not done in HAMSP-31).

The usage store sits behind a store interface ([ADR-0006](../adr/0006-usage-store.md)) and runs on its own CNPG cluster with TimescaleDB: hot and warm tiers in one hypertable, an hourly aggregate, 13 months of raw events ([ADR-0008](../adr/0008-usage-store-on-timescaledb.md)). The cold tier in S3 is v2. Duplicates are dropped by a unique key in that store: [ADR-0007](../adr/0007-usage-event-deduplication.md).
