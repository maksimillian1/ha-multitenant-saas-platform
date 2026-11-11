# ADR-0012: Tenant Management provisions a tenant synchronously, step by step, and records each step

Date: 2026-11-09

## Status

Proposed

Decided in HAMSP-6. Keeps [ADR-0003](0003-auth-service-implementation.md) (one Zitadel Organization per tenant, `tenant_id` = Organization ID), [ADR-0002](0002-data-isolation-strategy.md) (RLS in the shared database) and [ADR-0001](0001-system-boundaries.md), item 3 (a namespace `tenant-<id>` per tenant). Implemented in HAMSP-51.

## Context

FR-01: a platform admin creates, suspends and deletes a tenant through the API, with no manual steps. A tenant exists in several systems at once:

* a Zitadel Organization, whose ID is the `tenant_id` in every token (ADR-0003);
* a row in the tenant registry in PostgreSQL, and later the tenant's rows in other tables (ADR-0002);
* a namespace `tenant-<id>` with a ResourceQuota and Cilium policies (ADR-0001, item 3);
* rate limit and quota config and counters in the limits store (HAMSP-15, HAMSP-54);
* usage events in the usage store (ADR-0008).

These systems share no transaction. Any step can fail after an earlier one succeeded: the Organization exists but the namespace does not, or the process dies between two calls. Without a record of what was done, that leaves orphans nobody knows about.

There are two ways a tenant comes to exist:

1. **A platform admin creates it** in the admin app (ADR-0009). This ADR and HAMSP-51 cover this flow.
2. **A customer signs up** and their new account gets a tenant of its own. This flow is HAMSP-93 and is out of scope here, except that the lifecycle below must serve it unchanged (item 7).

Today the admin app creates and deletes Zitadel Organizations directly (HAMSP-34). That is a Zitadel-only operation, not a tenant: nothing else is provisioned or deleted.

## Decision

1. **Tenant Management owns the lifecycle.** It is the only service that creates, suspends or deletes a tenant. Every other system learns about tenants from it. The admin app calls Tenant Management's API and stops creating or deleting tenant Organizations in Zitadel itself.

2. **The tenant registry is the source of truth.** Tenant Management keeps a table `tenants` in the main PostgreSQL cluster: the tenant's `tenant_id`, its unique name, its state, the steps done so far, the last error and the timestamps. It is a platform table, not tenant data: only Tenant Management's role has privileges on it, and the RLS check lists it as a platform table.

3. **States:**

   ```
   provisioning ──► active ◄──► suspended ──► deleting ──► deleted
        │                                        ▲
        └────────────────────────────────────────┘  (delete a tenant stuck in provisioning)
   ```

   | State | Meaning | Login and API access | Data |
   |---|---|---|---|
   | `provisioning` | Tenant Management is creating the tenant's parts, or a step failed. | Organization may exist, no users yet. | Being created. |
   | `active` | Every part exists. | Accepted. | Kept. |
   | `suspended` | Blocked by a platform admin, reversible. | Organization deactivated: no logins, no new tokens. The API Gateway rejects tokens already issued within seconds (item 5). | Kept. |
   | `deleting` | Tenant Management is removing the tenant from every store, or a step failed. | Rejected. | Being deleted. |
   | `deleted` | Tombstone: `tenant_id`, timestamps, who deleted it. No other data. | Rejected. | Gone (QS-18). |

   The tombstone keeps a `tenant_id` from being reused and records that deletion finished. No `failed` state: a failed step leaves the tenant in `provisioning` or `deleting` with its last error, visible in the admin app, until an admin retries it or deletes the tenant.

4. **Provisioning is synchronous, step by step, with each step recorded.** No background worker.
   * `POST /tenants` first inserts the row in `provisioning` and commits it. Then, in the same request, it runs the steps in order: Zitadel Organization, first tenant admin, namespace with ResourceQuota and Cilium policies (later: limits config). After each step it records the result in the row. When the last step is done it sets `active` and answers `201` with the tenant.
   * When a step fails, the request stops, stores the error in `last_error` and answers with an error and the tenant, still in `provisioning`. It does not roll back the steps already done. The admin either retries (`POST /tenants/{id}/retry`), which resumes at the first step not done, or deletes the tenant (item 6).
   * If the process dies in the middle, the row stays in `provisioning` with the steps done so far; the admin sees it and retries or deletes it the same way.
   * Every step is idempotent, so a retry never creates a second Organization, user or namespace. The Organization step makes the `tenant_id`; to stay idempotent it first looks for an Organization an earlier attempt created (by an ID Tenant Management chooses and passes to Zitadel, or by the unique name if Zitadel does not accept one; HAMSP-51 checks which).
   * Two requests on the same tenant do not run at once: a request holds the tenant's lock (a PostgreSQL session advisory lock, released when the request ends or its process dies) and a second one gets `409`.

5. **Suspend and resume** are state changes with two effects: Tenant Management deactivates or reactivates the Organization in Zitadel, and the API Gateway reads the state and rejects a suspended tenant (HAMSP-52). Suspending keeps every piece of data.
   * Tenant Management publishes every tenant's state to the compacted Kafka topic `tenant-states`, keyed by `tenant_id`. It publishes after every operation and again once a minute for all tenants. A failed publish never fails the admin's request.
   * Every API Gateway replica reads the whole topic into memory and answers 403 to a tenant that is `suspended`, `deleting` or `deleted`. The request path makes no call. While Kafka is down, the last state read stays.
   * The deactivated Organization is the second layer. It gets no new tokens, so a token issued before the suspension stops working at the latest when it expires (`ACCESS_TOKEN_LIFETIME`, 60 s on kind), even if the event is lost.
   * Until Kafka has ACLs (HAMSP-31), only the network policy of the Kafka listener limits who may write to the topic.

6. **Deletion** runs the provisioning steps backwards, synchronously, the same way. Only a `suspended` tenant (or one stuck in `provisioning`) can be deleted, so deleting is always two steps and cannot happen by a single wrong click. Each store has an idempotent delete step: PostgreSQL rows, namespace, limits config and counters, usage events, Zitadel Organization last. When every step is done it writes the tombstone; when a step fails the tenant stays in `deleting` with its error until an admin retries. QS-18 sets the limit: 24 hours, so a tenant stuck in `deleting` is an alert, not a state to leave. Whether usage events of a deleted tenant are kept for billing disputes is decided in HAMSP-20; until then, deletion removes them.

7. **Every flow uses the same steps.** A tenant created by self-service sign-up (HAMSP-93) goes through the same states and steps. Only the trigger and its authorization differ. The API for platform admins requires the `super-admin` role; the sign-up flow will get its own entry point with its own checks.

8. **No new infrastructure.** The state lives in the `tenants` table; there is no worker, queue or workflow engine. If tenants stuck in a state become a problem, a background worker that retries them can be added later without changing the states or the steps.

## Considered options

| Option | Verdict |
|---|---|
| One synchronous call that rolls back what it created on error, with no record of steps | Rejected. A crash in the middle leaves orphans nobody knows about, and a failing rollback has no retry. |
| Asynchronous: `202` and a background worker that retries steps with backoff | Rejected for now (the owner, 2026-11-09). A few tenants a day do not need it: the steps take seconds, the admin waits for the answer, and a failure is retried by hand. Recovery without a person is what is given up. |
| Workflow engine (Temporal) | Rejected for now. Another stateful system to run highly available for a workflow of four or five steps that runs a few times a day (1 000 tenants in Sizing). Reconsider if workflows multiply. |
| Saga over Kafka (ADR-0005) | Rejected. Kafka is the usage pipeline; using it for commands adds topics, consumers and ordering rules for a low-volume flow that one table handles. |
| `Tenant` custom resource reconciled by an operator | Rejected. The registry would live in etcd, outside the PostgreSQL backups (QS-13) and out of reach of the API Gateway and SQL. FR-01 asks for an API, and C-04 GitOps does not apply to objects created at runtime per tenant. |
| Zitadel as the registry (Organization metadata holds the state) | Rejected. States and steps that are not Zitadel's would live in Zitadel; deleting the Organization would lose the record that deletion finished. |

## Consequences

**Good:**

* A partial failure always ends in a known state: the tenant row says which steps are done, and a retry finishes the rest or deletion cleans up.
* The admin gets the result in the answer: no polling, no background process to run and watch.
* Provisioning and deletion are one mechanism, testable with a fault injected at every step.
* Adding a new store (Kafka ACLs, a new database) is a new step in both directions.
* Self-service sign-up reuses all of it.

**Bad / risks:**

* Nothing retries on its own. A tenant left in `provisioning` or `deleting` waits for an admin; for deletion that can break the 24 hours of QS-18 unless someone watches for it.
* `POST /tenants` takes as long as all its steps, a few seconds, and has to fit within the timeouts of the admin app and the API Gateway.
* Every step must be idempotent, and every external system must let us find what an earlier run already created. This has to be tested per step.
* Tenant Management needs rights in Zitadel (create, deactivate, delete Organizations) and in Kubernetes (create and delete namespaces, ResourceQuotas and Cilium policies). It becomes a high-value target and is a critical feature (tenant boundaries).
* The admin app's direct "create" and "delete" of Organizations (HAMSP-34) must move to Tenant Management once it exists, or it creates Organizations that are not tenants and deletes tenants that leave data behind.
* The `tenants` table is the first table in the main cluster that is not tenant data. The RLS guardrail (`TestMigrationsLeaveNoUnprotectedTables`) needs an explicit list of platform tables, not a weaker check.
* The deletion of usage events stays open until HAMSP-20 settles retention.
