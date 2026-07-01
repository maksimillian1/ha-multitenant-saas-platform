# ADR-0002: Data Isolation Strategy for Multi-Tenant Architecture

Date: 2026-07-01

## Status
Proposed

## Context & Problem Statement
The platform must support multi-tenancy for B2B clients under high throughput (target: 5000+ RPS). We need to determine the optimal database isolation strategy in PostgreSQL managed by CloudNativePG (CNPG) to balance data security, operational complexity, infrastructure cost, and performance under peak loads.

The architecture must mitigate the "noisy neighbor" effect and guarantee that a failure or security breach in one tenant's context does not compromise or degrade the service of another.

## Decision Drivers
* **ROI & Operational Cost:** Minimizing cloud spend on managed database instances while maximizing tenant density per cluster.
* **Security & Compliance:** Strict data segregation enforced at the software or database layer.
* **Performance & Scalability:** Ability to scale storage and connection pools dynamically without manual sharding overhead.

## Considered Options
1. **Database-per-Tenant:** Separate PostgreSQL database for each tenant within the same CNPG cluster or separate clusters.
2. **Shared Database with Schema-per-Tenant:** One database, but distinct PostgreSQL schemas for each tenant.
3. **Shared Database with Row-Level Security (RLS):** Single database and schema, with strict tenant filtering enforced via native PostgreSQL RLS policies.

## Decision Outcome
**Chosen Option: Option 3 (Shared Database with Row-Level Security)**

### Consequences & Trade-offs
* **Positive (High ROI):** Maximum resource utilization. Connection pooling via PgBouncer is highly efficient since all tenants share the same connection pool, allowing the platform to hit 5000+ RPS without exhausting database backend workers. Simplified backup/restore topology managed globally via Barman Cloud to AWS S3.
* **Negative/Risks:** Requires flawless implementation of `tenant_id` context injection at the application level (Go/Node.js). High risk of noisy neighbor issues if a single tenant runs unoptimized queries; must be mitigated by strict API Gateway throttling and database-level statement timeouts.

### Implementation Checklist
1. Enable `ALTER TABLE ... ENABLE ROW LEVEL SECURITY` on all multi-tenant tables.
2. Implement custom PostgreSQL runtime configuration variable (e.g., `app.current_tenant_id`) injected via connection middleware from JWT context.
3. Define strict RLS policies: `CREATE POLICY tenant_isolation_policy ON table TO application_role USING (tenant_id = current_setting('app.current_tenant_id'));`
