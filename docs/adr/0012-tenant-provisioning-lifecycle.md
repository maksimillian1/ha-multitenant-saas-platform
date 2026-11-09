# ADR-0012: Tenants are provisioned by a resumable workflow in Tenant Management

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

These systems share no transaction. Any step can fail after an earlier one succeeded: the Organization exists but the namespace does not, or the process dies between two calls. A single synchronous call leaves orphans in that case and has no one to clean them up.

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

   | State | Meaning | Tokens | Data |
   |---|---|---|---|
   | `provisioning` | The workflow is creating the tenant's parts. | Organization may exist, no users yet. | Being created. |
   | `active` | Every part exists. | Accepted. | Kept. |
   | `suspended` | Blocked by a platform admin, reversible. | Organization deactivated: no logins, no new tokens. The API Gateway rejects tokens already issued (HAMSP-52). | Kept. |
   | `deleting` | The deletion workflow removes the tenant from every store. | Rejected. | Being deleted. |
   | `deleted` | Tombstone: `tenant_id`, timestamps, who deleted it. No other data. | Rejected. | Gone (QS-18). |

   The tombstone keeps a `tenant_id` from being reused and records that deletion finished. No `failed` state: a step that keeps failing leaves the tenant in `provisioning` or `deleting` with its last error, visible in the admin app, until it succeeds or an admin deletes the tenant.

4. **Provisioning is a resumable workflow, not a distributed transaction.**
   * `POST /tenants` writes the row in `provisioning` and answers `202` with the tenant. The admin app shows the state until it is `active`.
   * A worker in Tenant Management runs the steps in order: Zitadel Organization, tenant rows in PostgreSQL, namespace with ResourceQuota and Cilium policies, limits config. Each step is idempotent and records its result in the row before the next one starts.
   * On failure the worker retries the step with backoff. It does not roll back the steps already done: going forward is the recovery. A crash loses nothing, because the next run reads the row and resumes at the first step not done.
   * The Organization step makes the `tenant_id`. To stay idempotent it first looks for an Organization the workflow already created (by an ID Tenant Management chooses and passes to Zitadel, or by the unique name if Zitadel does not accept one; HAMSP-51 checks which).
   * Rollback of a tenant that should not exist is its deletion (item 6), the same workflow for a half-provisioned tenant as for a full one.

5. **Suspend and resume** are state changes with two effects: Tenant Management deactivates or reactivates the Organization in Zitadel, and the API Gateway reads the state and rejects a suspended tenant (HAMSP-52). Suspending keeps every piece of data.

6. **Deletion** is the provisioning workflow run backwards. Only a `suspended` tenant (or one stuck in `provisioning`) can be deleted, so deleting is always two steps and cannot happen by a single wrong click. Each store has an idempotent delete step: PostgreSQL rows, namespace, limits config and counters, usage events, Zitadel Organization last. The worker retries until every step is done, then writes the tombstone. QS-18 sets the limit: 24 hours. Whether usage events of a deleted tenant are kept for billing disputes is decided in HAMSP-20; until then, deletion removes them.

7. **Every flow uses the same workflow.** A tenant created by self-service sign-up (HAMSP-93) goes through the same states and steps. Only the trigger and its authorization differ. The API for platform admins requires the `super-admin` role; the sign-up flow will get its own entry point with its own checks.

8. **No new infrastructure.** The workflow state lives in the `tenants` table and the worker is part of Tenant Management. The worker claims a tenant with `SELECT … FOR UPDATE SKIP LOCKED`, so several replicas can run without stepping on each other (QS-10 survives a failover: the claim is released with the connection).

## Considered options

| Option | Verdict |
|---|---|
| One synchronous call that creates everything, rolls back what it created on error | Rejected. A crash in the middle leaves orphans, and a failing rollback has no retry. Fine for one Zitadel call, not for four systems. |
| Workflow engine (Temporal) | Rejected for now. Another stateful system to run highly available for a workflow of four or five steps that runs a few times a day (1 000 tenants in Sizing). Reconsider if workflows multiply. |
| Saga over Kafka (ADR-0005) | Rejected. Kafka is the usage pipeline; using it for commands adds topics, consumers and ordering rules for a low-volume flow that a table and a worker handle. |
| `Tenant` custom resource reconciled by an operator | Rejected. The registry would live in etcd, outside the PostgreSQL backups (QS-13) and out of reach of the API Gateway and SQL. FR-01 asks for an API, and C-04 GitOps does not apply to objects created at runtime per tenant. |
| Zitadel as the registry (Organization metadata holds the state) | Rejected. States and steps that are not Zitadel's would live in Zitadel; deleting the Organization would lose the record that deletion finished. |

## Consequences

**Good:**

* A partial failure always ends in a known state: the tenant row says which steps are done, and the worker finishes the rest or deletion cleans up.
* Provisioning and deletion are one mechanism, testable with a fault injected at every step.
* Adding a new store (Kafka ACLs, a new database) is a new step in both directions.
* Self-service sign-up reuses all of it.

**Bad / risks:**

* `POST /tenants` is asynchronous. The admin app has to show `provisioning` and poll, and a client cannot assume a tenant is usable when the call returns.
* Every step must be idempotent, and every external system must let us find what an earlier run already created. This has to be tested per step.
* Tenant Management needs rights in Zitadel (create, deactivate, delete Organizations) and in Kubernetes (create and delete namespaces, ResourceQuotas and Cilium policies). It becomes a high-value target and is a critical feature (tenant boundaries).
* The admin app's direct "create" and "delete" of Organizations (HAMSP-34) must move to Tenant Management once it exists, or it creates Organizations that are not tenants and deletes tenants that leave data behind.
* The `tenants` table is the first table in the main cluster that is not tenant data. The RLS guardrail (`TestMigrationsLeaveNoUnprotectedTables`) needs an explicit list of platform tables, not a weaker check.
* The deletion of usage events stays open until HAMSP-20 settles retention.
