# ADR-0001: System Boundaries & Multi-Tenant Core Components

Date: 2026-07-01

## Status

Proposed

## Context

Our high-availability B2B SaaS platform requires strict compute, network, and data isolation between tenants while maintaining a target throughput of 5000+ RPS under peak load. To maximize hardware utilization (high ROI) without introducing state corruption, security breaches, or cross-tenant data leaks, we must define clear boundaries between synchronous request processing, asynchronous ingestion, and the persistent data tier.

The system must natively handle unpredictable tenant load spikes (the "noisy neighbor" effect) and guarantee zero data loss (RPO=0) with rapid automated failover (RTO < 5s) during infrastructure degradation or AWS Spot Instance evictions.

## Decision

We establish the following system boundaries, architectural rules, and multi-tenant constraints:

1. **Ingress & Tenant Identification Boundary (API Gateway):**
   AWS ALB/NLB coupled with an Ingress Controller acts as the single entry point. Every incoming request must carry a cryptographically signed tenant token (JWT via Cognito/Auth0).
    * **Tenant Guardrails:** The ingress tier extracts the `tenant_id` from the token context. It enforces tenant-specific distributed rate-limiting and throttling at the entry point before traffic hits the application layer. Anonymous or malformed requests are dropped immediately.

2. **Synchronous App Tier Boundary (Go/Node.js API):**
   A stateless, always-on API deployment running in EKS, scaled dynamically by Karpenter.
    * **Compute Isolation:** To prevent a single tenant from exhausting cluster resources, pods utilize Kubernetes cgroups limits. Network-level isolation is strictly enforced via **Cilium Network Policies** (L4/L7), blocking unauthorized cross-tenant or cross-namespace pod communication.
    * **Context Propagation:** The application layer is entirely stateless. It extracts `tenant_id` and injects it into every database session context and message broker payload. Direct cross-tenant data access at the application memory level is prohibited.

3. **Asynchronous Ingestion Pipeline (Kafka & KEDA):**
   High-throughput, bursty operations (e.g., webhook ingestion like Stripe events) are immediately decoupled from the synchronous HTTP path.
    * **Data Ingestion Contract:** The API tier pushes incoming events directly into partitioned Apache Kafka topics. Topics use `tenant_id` as the routing key to guarantee strict event ordering per tenant.
    * **Event-Driven Scaling:** Consumer worker pools are managed as ephemeral deployments scaled by **KEDA** based on Kafka consumer lag metrics. If a specific tenant floods the system, KEDA scales up workers to process the lag without degrading the synchronous API response times.

4. **Isolated Data Tier Boundary (CloudNativePG & PgBouncer):**
   All relational data is consolidated into a PostgreSQL cluster managed by the CloudNativePG (CNPG) operator.
    * **Data Isolation Model:** We implement a **Shared Database with Row-Level Security (RLS)** model. A shared instance maximizes connection pooling efficiency via built-in PgBouncer, allowing us to hit 5000+ RPS without database backend worker exhaustion.
    * **Database Guardrails:** Every multi-tenant table must enforce `ALTER TABLE ... ENABLE ROW LEVEL SECURITY`. The application middleware must execute a session-level tenant configuration (e.g., `SET LOCAL app.current_tenant_id = '...'`) immediately upon acquiring a connection from the pool.

5. **Disaster Recovery & Storage Boundary (AWS S3 & Barman):**
   The database state is decoupled from live compute nodes. CNPG streams Write-Ahead Logs (WAL) continuously and pushes daily snapshots to an immutable, encrypted AWS S3 bucket via Barman Cloud.

## Consequences

* **Mitigated Noisy Neighbor Effect:** Tenant-specific throttling at the Ingress layer combined with Kafka partition isolation ensures that load spikes from Tenant A cannot starve Tenant B of compute or database resources.
* **Guaranteed RPO=0 & RTO < 5s:** By leveraging CNPG synchronous replication topologies, any sudden node eviction (e.g., AWS Spot termination) triggers an automated master election and promotion within 5 seconds. Live `k6` load tests with `Chaos Mesh` fault injection must continuously validate this metric.
* **Flawless RLS Dependency:** The architecture introduces a hard dependency on application-level discipline. If an engineer forgets to enable RLS on a new table or fails to inject the `app.current_tenant_id` context via the database connection driver, data leakage will occur. Static analysis tools and database schema migration checks must be implemented to block invalid schemas in CI/CD.
* **Connection Efficiency:** Utilizing a shared database topology allows PgBouncer to reuse server connections across multiple tenants seamlessly, minimizing PostgreSQL RAM overhead and significantly lowering cloud costs (High FinOps ROI).
* **Auditability:** Isolating data at the row level using explicit `tenant_id` fields simplifies global logging, data deletion compliance (GDPR/CCPA "Right to be Forgotten"), and backup operations, which are handled uniformly at the database level rather than via fragmented per-tenant backup scripts.
