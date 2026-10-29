# High-Availability Multi-Tenant SaaS Platform (HAMSP)

HAMSP is an infrastructure boilerplate for B2B SaaS products in which many customer companies (tenants)
share one Kubernetes cluster and one database.
It logs users in, keeps tenants apart, runs agent workloads and counts usage: API calls, agent tokens and network egress. Usage-based billing is planned for v2.

---

## Master Navigation Hub

Every phase MUST also read [Requirements & Constraints](architecture/requirements.md) (FR, QS, C). The `verify` phase checks the work against its quality scenarios (QS-xx).

Specific technical constraints must be sourced from the specialized files below:
- [Data Tier (Zero Data Loss, RPO=0, RLS)](architecture/data-tier.md)
- [Ingestion & Telemetry Pipeline (Async Load Leveling)](architecture/ingestion-pipeline.md)
- [Networking & Security (Hard Multi-Tenant Isolation, Cilium)](architecture/networking-security.md)
- [Control Plane (Automated Recovery, RTO < 5s)](architecture/control-plane.md)
- [Operations & Infrastructure (Terraform)](ops/terraform.md)
- [Contracts](contracts/)
- [C4 model](architecture/c4/README.md): diagrams generated from [workspace.dsl](architecture/c4/workspace.dsl)
- [Roadmap](roadmap.md): what belongs to v1 and v2

**Override Rule**: Accepted ADRs in [adr/](adr/) hold absolute priority over all other architectural documentation.

The ADR folder is the decision log. When an ADR changes something important in a subsystem, the subsystem file links to that ADR.

---

<h2 id="data-flow-diagram">Architecture Diagrams</h2>

The C4 model in [architecture/c4/workspace.dsl](architecture/c4/workspace.dsl) is the source of truth. The rendered diagrams are in [architecture/c4/README.md](architecture/c4/README.md).

* **System Context (L1):** platform admins, tenant admins and end users use HAMSP. HAMSP depends on the customer's identity provider, the Kubernetes API, AWS S3 and, in v2, a payment provider.
* **RequestPath (L2):** a request goes through the API Gateway, which validates the JWT, takes `tenant_id` and checks rate limits. It then reaches Tenant Management, ExampleApp or the Auth Service (Zitadel). Data lives in PostgreSQL with RLS.
* **UsagePipeline (L2):** the API Gateway (API calls), agent workloads (tokens) and the Egress Exporter send usage events to UsageWriteAPI. Events go through Kafka to Usage Writer, which drops duplicates and writes them to the usage store, a TimescaleDB cluster of its own (ADR-0008). Metering Query Service reads them back.

The Deployment view (namespaces, node pools, Karpenter, KEDA, Cilium) is not drawn yet.

---

<h2 id="directory-structure">Repository Structure</h2>

* `/docs` - Documentation: requirements, C4 model, ADRs (`/docs/adr`) and subsystem notes.
* `/prototype` - Local kind cluster with GitOps manifests (ArgoCD ApplicationSet, CloudNativePG, MinIO).
* `/apps` - Go services: `apps/gateway` (API Gateway, ADR-0004), `apps/usage` (UsageWriteAPI and Usage Writer), `apps/exampleapp` (the minimal per-tenant test service) and `apps/admin` (the super admin app, a Go BFF with a React SPA, ADR-0009).
* `/images` - Container images that are not Go services: `images/timescaledb`, the TimescaleDB extension image of the usage cluster (ADR-0008). It holds Timescale License (TSL) binaries; see [Data Tier](architecture/data-tier.md).
* `/terraform` - Infrastructure as Code for AWS EKS, Karpenter and core cloud dependencies. Empty for now.
* `/deploy` - Empty for now.
