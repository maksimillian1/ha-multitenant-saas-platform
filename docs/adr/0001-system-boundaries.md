# ADR-0001: System Boundaries & Multi-Tenant Core Components

Date: 2026-07-01

Updated: 2026-10-23. Synced with the C4 model ([`docs/architecture/c4/workspace.dsl`](../architecture/c4/workspace.dsl)) and [ADR-0003](0003-auth-service-implementation.md). See HAMSP-11 and HAMSP-13.

## Status

Proposed

## Context

Our high-availability B2B SaaS platform requires strict compute, network, and data isolation between tenants. At peak it must sustain 20 000 RPS (Sizing in [requirements.md](../architecture/requirements.md)). To maximize hardware utilization (high ROI) without introducing state corruption, security breaches, or cross-tenant data leaks, we must define clear boundaries between synchronous request processing, asynchronous usage ingestion, and the persistent data tier.

The system must natively handle unpredictable tenant load spikes (the "noisy neighbor" effect) and guarantee zero data loss (RPO=0) with rapid automated failover (RTO < 5s) during infrastructure degradation or AWS Spot Instance evictions.

## Decision

We establish the following system boundaries, architectural rules, and multi-tenant constraints:

1. **Ingress & Tenant Identification Boundary (API Gateway):**
   One AWS NLB passes external traffic to the API Gateway inside the cluster: Envoy proxy. It is the single entry point for external traffic. How Envoy is configured and run is worked out in HAMSP-17.
    * **Tenant Identity:** Every request carries a JWT issued by the Auth Service (Zitadel, ADR-0003). The Gateway validates it locally against the JWKS and takes `tenant_id` from it. Anonymous or malformed requests are dropped immediately.
    * **Tenant Guardrails:** The Gateway enforces per-tenant rate limits before traffic reaches the services. Rate limit and quota counters live in a shared store, Redis for now (HAMSP-15).
    * **API Usage:** The Gateway reports every API call as a usage event to UsageWriteAPI (item 4).

2. **Synchronous Service Tier Boundary (Go):**
   Platform services (Tenant Management, Metering Query Service, ExampleApp; Billing in v2) are stateless deployments shared by all tenants, running on EKS nodes provisioned by Karpenter.
    * **Compute Isolation:** Every pod has CPU and memory requests and limits. Network isolation is strictly enforced via **Cilium Network Policies** (L4/L7), blocking unauthorized cross-tenant or cross-namespace pod communication.
    * **Context Propagation:** Services take `tenant_id` only from the Gateway and inject it into every database session and every usage event. Direct cross-tenant data access at the application memory level is prohibited.
    * **ExampleApp:** a minimal service that reads and writes PostgreSQL per tenant. It exists to run the load and failure tests (QS-01, QS-04, QS-10) and is not part of the product.

3. **Tenant Namespaces for Agent Workloads:**
   Agent workloads (FR-04) run a tenant's code, so they do not share the service tier. Tenant Management creates a namespace `tenant-<id>` for every tenant, with a ResourceQuota and Cilium policies that block traffic to other tenant namespaces. How agents are started is still open (FR-04, HAMSP-18).
    * Agent workloads report their own usage (agent tokens) to UsageWriteAPI directly, not through the Gateway. UsageWriteAPI takes the tenant from the pod identity.

4. **Asynchronous Usage Pipeline (Kafka & KEDA):**
   Usage events (API calls, agent tokens, network egress; FR-05) are decoupled from the synchronous HTTP path.
    * **Ingestion Contract:** UsageWriteAPI checks the tenant's rate limit, writes the event to a partitioned Apache Kafka topic with `tenant_id` as the key (strict per-tenant ordering), and acknowledges only after Kafka has it (QS-07, QS-09).
    * **Writing:** Usage Writer consumes the topic, drops duplicates by `event_id` (HAMSP-7) and writes batches to the usage store (HAMSP-14). It updates the tenant's quota counters after a unique insert.
    * **Event-Driven Scaling:** Usage Writer is scaled by **KEDA** on Kafka consumer lag. If a specific tenant floods the system, lag grows and KEDA adds consumers; UsageWriteAPI latency for other tenants is unaffected (QS-08). If the usage store is down, events wait in Kafka (QS-09).

5. **Isolated Data Tier Boundary (CloudNativePG & PgBouncer):**
   All relational data is consolidated into a PostgreSQL cluster managed by the CloudNativePG (CNPG) operator. Usage events are not stored here (item 4).
    * **Data Isolation Model:** We implement a **Shared Database with Row-Level Security (RLS)** model (ADR-0002). A shared instance maximizes connection pooling efficiency via built-in PgBouncer, allowing us to hit 5000+ RPS without database backend worker exhaustion.
    * **Database Guardrails:** Every multi-tenant table must enforce `ALTER TABLE ... ENABLE ROW LEVEL SECURITY`. The application middleware must execute a session-level tenant configuration (e.g., `SET LOCAL app.current_tenant_id = '...'`) immediately upon acquiring a connection from the pool.
    * **Auth Data:** Zitadel uses its own database in the same CNPG cluster, separate from application data (ADR-0003).

6. **Disaster Recovery & Storage Boundary (AWS S3 & Barman):**
   The database state is decoupled from live compute nodes. CNPG streams Write-Ahead Logs (WAL) continuously and pushes daily snapshots to an immutable, encrypted AWS S3 bucket via Barman Cloud.

## Consequences

* **Mitigated Noisy Neighbor Effect:** Per-tenant rate limits at the Gateway and UsageWriteAPI, combined with Kafka partitioning by tenant, ensure that load spikes from Tenant A cannot starve Tenant B of compute or database resources.
* **Shared Limits Store in the Hot Path:** Every API call and every usage event checks the rate limit store. Its latency and failure behaviour (fail open or fail closed) directly affect QS-03, QS-07 and QS-08 and must be settled in HAMSP-15.
* **Guaranteed RPO=0 & RTO < 5s:** By leveraging CNPG synchronous replication topologies, any sudden node eviction (e.g., AWS Spot termination) triggers an automated master election and promotion within 5 seconds. Live `k6` load tests against ExampleApp with `Chaos Mesh` fault injection must continuously validate this metric.
* **Flawless RLS Dependency:** The architecture introduces a hard dependency on application-level discipline. If an engineer forgets to enable RLS on a new table or fails to inject the `app.current_tenant_id` context via the database connection driver, data leakage will occur. Static analysis tools and database schema migration checks must be implemented to block invalid schemas in CI/CD.
* **Connection Efficiency:** Utilizing a shared database topology allows PgBouncer to reuse server connections across multiple tenants seamlessly, minimizing PostgreSQL RAM overhead and significantly lowering cloud costs (High FinOps ROI).
* **Auditability:** Isolating data at the row level using explicit `tenant_id` fields simplifies global logging, data deletion compliance (GDPR/CCPA "Right to be Forgotten"), and backup operations, which are handled uniformly at the database level rather than via fragmented per-tenant backup scripts. Deleting a tenant (FR-01) must also cover its usage events, its counters in the limits store and its namespace.
