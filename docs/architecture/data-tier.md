# Data Tier Architecture & Constraints

[Template: Populate with data tier specific documentation, constraints, RLS policies, PostgreSQL configuration, CloudNativePG, etc.]

Two CNPG clusters, both with 3 instances, quorum synchronous replication (ANY 1), a PgBouncer pooler in transaction mode and Barman backups to S3:

* **Main cluster** (`main-pg-cluster`): tenant and application data with forced RLS ([ADR-0002](../adr/0002-data-isolation-strategy.md)) and Zitadel's database ([ADR-0003](../adr/0003-auth-service-implementation.md)).
* **Usage cluster** (`usage-pg-cluster`): the `usage` database on PostgreSQL 18 with TimescaleDB, platform-only ([ADR-0008](../adr/0008-usage-store-on-timescaledb.md)). Usage events are a hypertable with 1-day chunks: uncompressed for 8 days (hot), columnstore after that (warm), dropped after 13 months; an hourly continuous aggregate holds the sums per tenant and metric. Application roles have no privileges on its tables: Usage Writer writes through `write_usage_events`, readers use the `*_tenant` views. It started in the main cluster ([ADR-0006](../adr/0006-usage-store.md)).

The CNPG operator is 1.30. Both clusters still use the in-tree Barman backup, which CNPG removes in 1.31 (HAMSP-29).

**Licence:** TimescaleDB's columnstore, continuous aggregates and policies are under the Timescale License (TSL), not Apache 2.0. The TSL allows using them inside our own service; it does not allow offering TimescaleDB as a database service. The extension image is built from `images/timescaledb`; everything else in the data tier is open source under OSI licences.
