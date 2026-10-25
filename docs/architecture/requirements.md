# Requirements & Constraints

The architecture drivers of HAMSP: what the platform must do, how well it must do it, and within which limits.
ADRs cite these IDs in their decision drivers, and the `verify` phase checks work against the quality scenarios (QS-xx).

---

## 1. Functional Requirements

Core requirements for system architecture

| ID | Requirement | Source |
|---|---|---|
| FR-01 | A platform admin creates, suspends and deletes a tenant through the API, with no manual steps. Deleting a tenant removes all of its data. | C4 model, ADR-0001; HAMSP-6 |
| FR-02 | A tenant admin manages the tenant's own users, roles, SSO and login branding. | ADR-0003 |
| FR-03 | A tenant's end users and API clients call the platform through the API Gateway with a token that identifies the tenant. | ADR-0001 |
| FR-04 | A tenant can start isolated agent workloads in the cluster. | C4 model, ADR-0001 |
| FR-05 | Usage events go to UsageWriteAPI: the API Gateway reports API calls, agent workloads report agent tokens, and the platform reports network egress. The platform stores them per tenant and exposes them for reading. | C4 model, ADR-0001; HAMSP-9 |
| FR-06 | Billing is a separate feature which is not in v1 scope, but generally it will have 2 strategies Pay per usage or Subscription package limit, both built on the stored usage records from FR-05. v1 must keep usage records complete and deduplicated enough to bill from (QS-06, QS-09). | none |
| FR-07 | The platform enforces per-tenant rate limits and usage quotas, configured per tenant. | QS-03, QS-15, QS-16, FR-06 |

---

## 2. Quality Requirements

### Quality goals

Ordered by priority, so when two goals conflict, the higher one wins.

1. **Tenant isolation:** no tenant can read another tenant's data or degrade its service (QS-01, QS-02, QS-03, QS-12, QS-15). A deleted tenant leaves no data behind (QS-18).
2. **Data durability:** an acknowledged write to the client is never lost or counted twice while at least one replica of the database or of the Kafka topic survives (QS-04, QS-06, QS-09, QS-17). If the whole cluster is lost, at most the last 5 minutes of writes are lost (QS-13).
3. **Availability:** the platform survives node, database and dependency failures and deploys without manual action (QS-04, QS-05, QS-09, QS-10, QS-11, QS-13, QS-16, QS-17).
4. **Cost:** a running environment fits the budget in C-02 +- 15% (QS-14).
5. **Throughput:** the API Gateway and UsageWriteAPI sustain their target load (QS-04, QS-07, QS-08).

### Quality scenarios

Each scenario is a test; the status shows its last run.
Scenarios that need application traffic run it against **ExampleApp**, a minimal test service behind the API Gateway that reads and writes PostgreSQL per tenant.

| ID    | When                                                                                 | The system must                                                                                                       | How we verify                                                                                             | Status     |
|-------|--------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------|------------|
| QS-01 | Tenant A, with a valid token, requests data that belongs to tenant B.                | Return none of tenant B's rows (empty result or 404).                                                                 | Integration test with two tenants. CI check that every multi-tenant table has RLS enabled.                | Pass (2026-10-25, HAMSP-25) |
| QS-02 | A workload of tenant A opens a network connection to a workload of tenant B.         | Block the connection.                                                                                                 | Cilium network policy test.                                                                               | Not tested |
| QS-03 | Tenant A sends 10× its rate limit while tenant B runs normal load.                   | Answer tenant A with 429. Tenant B's p95 latency grows by no more than 25%.                                           | k6, two load profiles at once.                                                                            | Not tested |
| QS-04 | The primary PostgreSQL instance is killed (pod deleted or node lost) under 5000 RPS. | Accept writes again within 5 s. Lose no write that got a 2xx. Keep 5xx below 1% during the failover.                  | k6 against ExampleApp + Chaos Mesh. After the run, compare acknowledged write IDs with the database. | Not tested |
| QS-05 | AWS sends a Spot interruption notice for a node running API pods.                    | Move the pods to other nodes within the 2-minute notice. Error rate stays below 1%.                                   | k6 + a simulated Spot interruption.                                                                       | Not tested |
| QS-06 | The same usage event arrives 3 times (client retries after a timeout).               | Count it once: one record in usage storage.                                                                           | Integration test that resends the same event.                                                             | Pass (2026-10-25, HAMSP-25) |
| QS-07 | Steady 20 000 RPS on UsageWriteAPI for 30 minutes.                                   | p95 latency below 50 ms, error rate below 0.1%. Consumer lag does not grow over the run.                              | k6 + Kafka consumer lag metrics.                                                                          | Not tested |
| QS-08 | One tenant sends 10× its normal usage event rate for 1 minute.                       | Other tenants' UsageWriteAPI p95 stays below 50 ms. Consumer lag drains to zero within 5 minutes after the burst.         | k6 + Kafka consumer lag metrics.                                                                          | Not tested |
| QS-09 | Usage storage is unavailable for 5 minutes while usage events keep arriving.         | Keep accepting usage events (2xx). Store every accepted event once storage is back.                                   | k6 + Chaos Mesh; count accepted vs stored events.                                                         | Pass at low load on kind (2026-10-25, HAMSP-25); load run on EKS pending |
| QS-10 | A new API version is rolled out under 5000 RPS.                                      | No request fails because of the rollout: 5xx below 0.1%.                                                              | k6 during a rolling deploy.                                                                               | Not tested |
| QS-11 | Zitadel is unavailable for 10 minutes.                                               | Requests with already-issued tokens keep working. New logins fail with a clear error.                                 | Chaos Mesh + k6.                                                                                          | Not tested |
| QS-12 | Tenant A runs a query that takes minutes.                                            | Cancel the query after 30 s. Tenant B's p95 latency is unaffected.                                                    | Integration test + k6.                                                                                    | Not tested |
| QS-13 | The PostgreSQL cluster is lost entirely.                                             | Restore from the S3 backup into a new cluster within 30 minutes. Lose at most the last 5 minutes of committed writes. | Scheduled restore drill.                                                                                  | Not tested |
| QS-14 | The environment runs for a month at the average load from Sizing.                    | The AWS bill stays within the C-02 budget +- 15%.                                                                     | AWS Budgets alert on the environment's cost tag; monthly Cost Explorer check.                             | Not tested |
| QS-15 | Tenant A reaches its monthly usage quota while still sending traffic. | Reject tenant A's further requests with 429 within 60 s. Other tenants are unaffected. | Integration test with a low quota + k6. | Not tested |
| QS-16 | The rate limit store is unavailable for 5 minutes under 20 000 RPS. | Keep serving requests: no 5xx caused by the store, error rate below 1%. Limits fall back to per-replica limits. Counters recover without manual action when the store is back. | k6 + Chaos Mesh. | Not tested |
| QS-17 | One Kafka broker is lost (pod deleted or node lost) under 20 000 RPS on UsageWriteAPI. | Lose no usage event that got a 2xx. Keep accepting events; error rate below 1% during leader election. | k6 + Chaos Mesh. After the run, compare acknowledged event IDs with the usage store. | Pass at low load on kind (2026-10-25, HAMSP-25); load run on EKS pending |
| QS-18 | A platform admin deletes tenant A (FR-01). | Within 24 hours no data of tenant A remains: PostgreSQL rows, Zitadel organization, usage events, rate limit and quota counters, tenant namespace. Copies in Kafka expire with the topic retention. | Integration test: create a tenant with data in every store, delete it, search every store for its tenant_id. | Not tested |

**Sizing:** 1 000 tenants, each a small business of 2–10 people. At peak a tenant sends up to 20 requests/s, 20 000 RPS in total. Every API call through the API Gateway becomes a usage event, and agent workloads also write to UsageWriteAPI directly, so UsageWriteAPI takes up to 20 000 RPS. QS-04 and QS-10 use 5 000 RPS against ExampleApp as their load profile. The targets assume all tenants peak at once. Average load is ~10% of peak (~2 000 usage events/s, ~170 M events/day). Usage events are kept for 13 months (billing disputes, v2).

---

## 3. System Constraints

Things that are not up for debate. A technology choice that could reasonably go another way belongs in an ADR, not here.

| ID   | Constraint | Why                   |
|------|---|-----------------------|
| C-01 | Runs on AWS (`eu-central-1`) on Kubernetes (EKS). | EU oriented                      |
| C-02 | A running environment costs no more than `TODO` USD per month. It can be torn down when not in use. | Personal budget.      |
| C-03 | Project code is MIT-licensed. Nothing under `/apps` may depend on AGPL code; CI enforces this. | `LICENSE`, ADR-0003.  |
| C-04 | Infrastructure is created only through Terraform and GitOps (ArgoCD). No manual changes in the cluster. | Reproducibility.      |
| C-05 | Non arguable techstack: terraform, k8s, cnpg, kafka. They are not re-argued in ADRs. | High performance tech |
| C-06 | Runs on arm64 (AWS Graviton) wherever possible. Every image we build is multi-arch (`linux/amd64` and `linux/arm64`); a third-party component without an arm64 image needs a stated reason and runs on an amd64 node pool. CI checks that `/apps` builds for arm64. | Cost (C-02, QS-14); HAMSP-27. |
 
