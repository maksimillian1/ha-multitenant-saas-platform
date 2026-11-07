# Roadmap

What gets built and in what order. Each line points to the decision or issue that holds the details. Status lives in Linear (team `HAMSP`), not here.

Two parts:

1. **Local prototype** on the kind cluster: every v1 feature works and passes the automated gates. It ends with `HAMSP-23-platform-skeleton` merged to `master`.
2. **AWS:** Terraform, EKS, load and chaos tests. The quality scenarios that need real load or real AWS (Spot, S3 restore, cost) are tested here.

## Feature stages

Agents write most of the code, so a feature ships in stages: `proto`, `alpha`, `beta`, `stable`. Up to `beta` it is checked by agents and automated guardrails; `stable` means the owner has read the code.

What each stage promises, which guardrails it needs and how features move between stages: [Feature stages](process/feature-stages.md). The decision: [ADR-0011](adr/0011-feature-stages.md).

## Part 1: local prototype (kind)

### Phase 0: development process

Without it, agent-shipped code is not under control.

* Feature map: one file per feature in `docs/features/` with its stage, code, checklist and reviews, and a generated map of all features (HAMSP-45). First: the other guardrails depend on it.
* Pre-commit hooks and agent rules (HAMSP-40, HAMSP-44).
* Feature flags, optional until a feature needs one (HAMSP-63). `feature-verify` checks the gates of a stage (HAMSP-64).
* AGPL check in CI (HAMSP-21, C-03), arm64 builds (HAMSP-27, C-06).

### Phase 1: close the gaps in the existing core

The core is the request path (HAMSP-5) and the usage pipeline (HAMSP-25, HAMSP-26). It is the base for every feature.

* Platform API audience restricted to tenant organizations (HAMSP-24).
* Kafka authentication and ACLs for the usage topic (HAMSP-31).
* Short-lived credentials for the admin app service account (HAMSP-36).
* Flaky database tests in `apps/usage` (HAMSP-32).
* Usage Writer DLQ for poison records (HAMSP-30).

### Phase 2: the rest of v1 as `alpha`, then `beta`

The owner decides first; agents implement after the decision.

* **Tenant Management (FR-01):** decision HAMSP-6, then the service (HAMSP-51). The API Gateway rejects a suspended tenant (HAMSP-52). Tenant deletion removes data from every store (QS-18, HAMSP-53), and the conflict with 13-month usage retention is settled (HAMSP-20).
* **Per-tenant rate limits and quotas (FR-07):** store decision HAMSP-15, then enforcement in the API Gateway and the source of each tenant's limits (HAMSP-54; QS-03, QS-16 at low load). How quotas reach the gateway within 60 s is a decision of its own (HAMSP-55, QS-15).
* **Metering Query Service (FR-05):** reads through the same store interface; its read API follows HAMSP-9 (HAMSP-56).
* **Admin app:** Zitadel organizations, users and role assignments (HAMSP-34). Tenant admins manage their own users, roles and SSO (FR-02, HAMSP-57).
* **Network isolation (QS-02):** a namespace per tenant and default-deny Cilium policies between them, tested on kind (HAMSP-58).
* **Resilience scenarios that kind can test:** Zitadel unavailable (QS-11, HAMSP-60), long queries cancelled after 30 s (QS-12, HAMSP-59).
* **Agent workloads (FR-04)** and **egress measurement (FR-05):** decisions HAMSP-18 and HAMSP-19. These are the least certain and may stay at `proto` or `alpha` longest.

### Phase 3: harden to `beta` and merge

* End-to-end tests in CI: admin app against a real Zitadel (HAMSP-48), the kind suite with API tests through the gateway (HAMSP-49).
* Coverage floor and diff coverage (HAMSP-43), security scanners (HAMSP-46), authorization matrix and fuzz targets (HAMSP-47), control canaries for critical controls (HAMSP-50).
* Observability: metrics, Kafka consumer lag, logs with `tenant_id` (HAMSP-61). The chaos runs and the load scenarios need it.
* Chaos Mesh on kind at low load: functional checks of QS-04, QS-09 and QS-17 (HAMSP-62).
* Rolling deploys without failed requests: PDBs, readiness, preStop (QS-10, HAMSP-65). Restore drill from MinIO (QS-13, HAMSP-66).
* Usage Writer autoscaled on consumer lag with KEDA (HAMSP-69); may move to part 2.
* How secrets are kept out of the GitOps manifests is decided (HAMSP-68); it is implemented in part 2.
* Every v1 feature is at `beta` or explicitly left lower and switched off by default.
* `HAMSP-23-platform-skeleton` is merged to `master` against its checklist (HAMSP-67). This is the v1 prototype. Manual review has not happened yet; it is the next track.

**Exit criteria:** every v1 FR is at `beta` or switched off. Every quality scenario that needs no load is `Pass` on kind.

## Stabilization: `beta` → `stable`

Runs after the merge, in parallel with part 2. The owner reads the code feature by feature, bottom-up:

1. Critical features of the core: JWT validation and tenant context in the API Gateway, RLS and migrations, UsageWriteAPI ack and deduplication.
2. The rest of the core: ExampleApp, Usage Writer, the usage store.
3. Features from phase 2, in the order they depend on each other.

A feature becomes `stable` when it is reviewed, its flag, if it has one, is removed and the quality scenarios it touches pass. Scenarios that need load pass only after part 2.

## Part 2: AWS

### Phase 4: infrastructure

* The C-02 budget gets a number; node pool sizes follow from it.
* Terraform: VPC, EKS, Karpenter on Graviton, S3, IAM ([Terraform](ops/terraform.md)). ArgoCD deploys the same manifests as on kind (C-04).
* One command creates the environment and one tears it down (C-02). Budget alert on the cost tag (QS-14).
* CNPG backups on the Barman Cloud Plugin (HAMSP-29). Envoy as the API Gateway behind the NLB (HAMSP-17).
* The EKS staging environment runs only `beta` and `stable` features. `proto` and `alpha` stay on kind.

### Phase 5: load and chaos on EKS

* k6 and Chaos Mesh: QS-03, QS-04, QS-05, QS-07, QS-08, QS-10, QS-13, QS-16.
* Load runs of QS-09 and QS-17, which so far pass only at low load on kind.

## v1 scope

The thinnest working version of every core service:

* Request path with tenant isolation: Cilium Gateway API → API Gateway → ExampleApp → PostgreSQL with RLS (HAMSP-5, [ADR-0002](adr/0002-data-isolation-strategy.md), [ADR-0004](adr/0004-api-gateway-go-service.md)).
* Tenant Management (FR-01): after the tenant lifecycle decision (HAMSP-6).
* Usage pipeline (FR-05): UsageWriteAPI → Kafka on Strimzi ([ADR-0005](adr/0005-kafka-on-strimzi.md)) → Usage Writer → usage store behind a store interface ([ADR-0006](adr/0006-usage-store.md)), duplicates dropped by a unique key ([ADR-0007](adr/0007-usage-event-deduplication.md)).
* Usage store on its own CNPG cluster with TimescaleDB: hot and warm tiers, hourly aggregate, CNPG 1.30 (HAMSP-25, [ADR-0008](adr/0008-usage-store-on-timescaledb.md)).
* Metering Query Service (FR-05): reads through the same store interface; its read API follows HAMSP-9.
* Per-tenant rate limits and quotas in the API Gateway (FR-07): after HAMSP-15.

## v2

* **Cold usage tier:** old chunks exported to S3, probably Parquet, queried on demand ([ADR-0006](adr/0006-usage-store.md)); hot and warm are in place since HAMSP-25 ([ADR-0008](adr/0008-usage-store-on-timescaledb.md)). Deduplication must be revisited for it ([ADR-0007](adr/0007-usage-event-deduplication.md)).
* **Tenant access to usage:** only through derived data (TimescaleDB jobs, an export to a tenant-facing store), never through the usage cluster (ADR-0008).
* **Billing** (FR-06) and the payment provider.
