# Integrating a business app

How a business application runs on HAMSP: what the platform gives it, what the app must do, and how it is connected and upgraded. The decision behind it is [ADR-0013](adr/0013-business-app-integration.md); where this guide and the ADR differ, the ADR wins.

A **business app** is a product that runs for many tenants on the platform: a CRM, an LMS, a helpdesk, written by its own team or forked from an open-source project. It lives in its **own repository** and is never copied into this one. The platform's own test services (`apps/exampleapp`, `apps/exampleweb`) are not business apps.

> **Status.** Most of this guide describes the target of HAMSP-95. The table below says what exists today. Sections about parts that do not exist yet describe the planned shape, and may change when those parts are built.

| Part | Status |
|---|---|
| Tenant in `x-tenant-id` from the API Gateway, for bearer tokens | runs ([contract](contracts/gateway-headers.md)) |
| Browser sign-in and user headers for apps | planned, HAMSP-100 |
| App registry, ApplicationSet, AppProject per app | planned, HAMSP-97 |
| Library Helm chart | planned, HAMSP-98 |
| Platform-owned network policy, database roles, Kyverno rules | planned, HAMSP-99 |
| Tenant events for apps | planned, HAMSP-101 (Tenant Management already publishes `tenant-states`, ADR-0012) |
| Conformance tests and chart validation in platform CI | planned, HAMSP-102 |
| First connected app (showcase) | planned, HAMSP-103 |

## 1. The picture

```
                        HAMSP repository                      App repository
                 (Terraform, Argo CD, platform,         (code, Dockerfile, chart on the
                  library chart, app registry)            library chart, migrations)
                              │                                     │
                              └──────────────┐    ┌─────────────────┘
                                             ▼    ▼
                                           Argo CD
                                             │
   ┌─────────────────────────────────────────┼──────────────────────────────────────────┐
   │ cluster                                 ▼                                          │
   │                                                                                    │
   │  browser ──► Cilium Gateway ──► API Gateway / BFF ──► namespace app-<name>         │
   │                                  │  sign-in (Zitadel)      │  your Deployment      │
   │                                  │  sets x-tenant-id,      │                       │
   │                                  │  x-user-*, drops token  ├──► CNPG: your database│
   │                                  │                         │    (RLS)              │
   │                                  │                         ├──► Kafka: usage events│
   │                                  │                         ◄─── Kafka: tenant events
   └────────────────────────────────────────────────────────────────────────────────────┘
```

The platform and your app have separate lifecycles. The platform is upgraded without touching your app; your app is deployed without touching the platform. They meet only at the contract below.

## 2. The contract

### Identity

* Every request reaches your app through the API Gateway. It carries `x-tenant-id` (the tenant, a Zitadel Organization ID) and, once HAMSP-100 is done, the user: `x-user-id`, `x-user-email`, `x-user-roles`.
* The token never reaches your app. Do not validate JWTs, do not keep a sign-in of your own, do not keep passwords.
* Take the tenant **only** from `x-tenant-id`. Never from the path, the query, the body, a cookie or a subdomain.
* A request without `x-tenant-id` gets 401.
* Sign-out sends the user to the platform's sign-out.

The header is safe to trust only because nothing but the Gateway can reach your namespace (section 5). Do not expose your Service in any other way.

### Data

* Your app gets a database in the platform's CNPG cluster and two roles:
  * an **app role** for the running app: no `BYPASSRLS`, does not own the tables;
  * an **owner role** for migrations only.
* Every table that holds tenant data has a `tenant_id` column, and RLS **enabled and forced**:

  ```sql
  ALTER TABLE items ENABLE ROW LEVEL SECURITY;
  ALTER TABLE items FORCE ROW LEVEL SECURITY;
  CREATE POLICY tenant_isolation ON items
    USING (tenant_id = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.current_tenant_id', true));
  ```

  A missing setting gives `NULL`, which matches no row: a query without a tenant sees nothing.
* Every request runs its queries **in one transaction**, and the first statement sets the tenant for that transaction:

  ```sql
  SELECT set_config('app.current_tenant_id', $1, true);  -- true = for this transaction only
  ```

  The pool is shared between tenants (PgBouncer in transaction mode), so a session-level `SET` would leak the tenant to the next request. ORMs that open their own connections or transactions need a wrapper; check it with a test.
* A record of another tenant is answered with **404**, never 403, so record IDs cannot be probed.
* Background jobs do not pass through request middleware. Each job carries its tenant and sets it the same way.
* Data outside Postgres is outside RLS: caches, queues, files, search indexes, websocket rooms. Put the tenant in every key, path or channel name, and check it on read.

### Tenants

* Tenant Management owns the tenant lifecycle ([ADR-0012](adr/0012-tenant-provisioning-lifecycle.md)). Your app does not create or delete tenants.
* Your app learns about tenants from tenant events (HAMSP-101): it sets up a new tenant's data when the tenant appears, and deletes all of it when the tenant is deleted. Tenant deletion (FR-01, QS-18) must remove your app's data too.
* Until HAMSP-101, an app may create a tenant's data on the tenant's first request.

### Usage

* Your app reports what its tenants use as usage events in the schema of [ADR-0010](adr/0010-usage-event-contracts.md), keyed by `tenant_id`.
* The library chart gives your app a topic and a Kafka user (section 3).

### Network

* Your app listens on one HTTP port. Only the API Gateway can reach it.
* Outgoing calls go only to what your app declared (the database, Kafka, external APIs you list). The platform's policy closes everything else.

### Runtime

* One image, built for `linux/amd64` and `linux/arm64` (C-06).
* Liveness and readiness endpoints; graceful shutdown on `SIGTERM`.
* Logs as JSON on stdout, with `tenant_id` on every line that concerns a tenant.
* Configuration from environment variables and mounted secrets; no secrets in the image.

## 3. Your repository

```
my-app/
├── src/ …                     your code
├── Dockerfile
├── migrations/                run as the owner role, by a Job before the Deployment
└── deploy/
    └── chart/
        ├── Chart.yaml         depends on the HAMSP library chart
        ├── values.yaml        what your app needs from the platform
        └── templates/         your Deployment, Service, Job; the library renders the rest
```

`Chart.yaml` (planned shape, HAMSP-98):

```yaml
apiVersion: v2
name: my-app
version: 0.1.0
dependencies:
  - name: hamsp-app
    version: "~0.1"
    repository: oci://<registry>/hamsp/charts
```

`values.yaml` (planned shape, HAMSP-98):

```yaml
hamsp:
  app: my-app
  contract: v1alpha1            # the contract version your app was built for
  route:
    host: my-app.example.com
    paths: ["/"]
  database:
    name: my_app                # app and owner roles are created by the platform
  kafka:
    produces: [usage-events]
    consumes: [tenant-states]
  egress:
    - host: api.example-provider.com
      port: 443
image:
  repository: ghcr.io/me/my-app
  tag: 1.4.2
```

The library chart renders from this the CNPG `Database`, Strimzi `KafkaTopic` and `KafkaUser`, the route, standard labels, env (`DATABASE_URL`, Kafka settings), probes and resources. You do not write these resources yourself: when the platform upgrades CNPG or Strimzi, the library chart changes and your chart stays as it is.

### Forking an open-source app

Most open-source apps were not built for this contract. A fork usually needs:

1. **Its own repository.** An AGPL app cannot live in the HAMSP repository (C-03). AGPL also requires that users of a modified version can get its source; keep the fork public and link it in the UI.
2. **Sign-in replaced** by the platform's headers: create or update the user from `x-user-id`, turn off the app's own sign-in, sign-up, password reset and OAuth.
3. **Tenant source replaced:** the app's tenant, organization or workspace comes from `x-tenant-id`, not from a subdomain or a picker. Map the platform tenant to the app's own ID if they differ.
4. **RLS added** to every tenant table, and `set_config` in every transaction. If the app's tables reach the tenant only through joins, add a `tenant_id` column first.
5. **Everything outside Postgres** given a tenant: caches, queues, files, realtime channels.
6. **Paid features avoided.** If the app is open-core and its multi-tenancy or SSO is under a commercial license, do not copy that code; write your own.

## 4. Connecting your app

Planned shape, HAMSP-97. A pull request to the HAMSP repository adds one entry to the app registry:

```yaml
- name: my-app
  repoURL: https://github.com/me/my-app.git
  path: deploy/chart
  targetRevision: main
```

From it the platform creates:

* a namespace `app-my-app`;
* an AppProject that lets Argo CD deploy only into that namespace and only the resource kinds the library chart and your templates need;
* an Argo CD Application that pulls your chart from your repository and keeps the namespace in sync;
* the database roles in CNPG and the Zitadel project and roles, both owned by the platform.

Nothing is applied by hand in the cluster (C-04). Removing an app is a pull request too.

After that, your app is deployed from your own repository: CI builds the image, the new tag lands in your chart's values, Argo CD syncs it.

## 5. What your app may and may not create

| Your chart may create | Your chart may not create |
|---|---|
| Deployment, Service, ConfigMap, Secret, Job, CronJob, HorizontalPodAutoscaler, PodDisruptionBudget | `CiliumNetworkPolicy`, `CiliumClusterwideNetworkPolicy`, `NetworkPolicy` |
| The resources the library chart renders (CNPG `Database`, Strimzi `KafkaTopic` and `KafkaUser`, the route) | Database roles, or anything in the CNPG `Cluster` |
| | Anything outside its namespace |
| | RBAC, CRDs, admission webhooks, Services of type `LoadBalancer` or `NodePort` |

This is not a convention. It is enforced (HAMSP-99):

* the AppProject rejects these kinds at sync;
* a Kyverno rule rejects them from anyone but the platform, so a manual `helm install` cannot bypass the AppProject;
* a cluster-wide Cilium policy of the platform denies all traffic into `app-*` namespaces except from the API Gateway. Deny rules win over allow rules, so even a policy that slips through cannot open your app.

Why it matters: if anything other than the Gateway can reach your app, anyone can send a forged `x-tenant-id` and read another tenant's data.

If your app needs an exception, open an issue; the platform decides.

## 6. Platform upgrades

* HAMSP and the library chart have semantic versions. Your chart pins a range (`~0.1`).
* A breaking change (an API version removed, a library value removed) is a major release, after a period in which the old and the new way both work.
* Before every platform release, platform CI renders the chart of every registered app with the new library chart and validates it against the new CRDs (HAMSP-102). A change that would break your next deploy is found before it reaches the cluster.
* A running app keeps running through an operator upgrade: Kubernetes converts stored resources between versions. What can break is the next sync, which is what the check above protects.

## 7. Checks your app must pass

The platform runs these against your app after each deploy (HAMSP-102). An app that fails them is not ready.

| Check | What it does |
|---|---|
| T-01 Tenant boundaries | Two tenants. Tenant A reads, changes and deletes tenant B's records and gets 404 or an empty result. Requests without a tenant, or with a forged one, get nothing. |
| T-04 Authorization | Every endpoint, for every role, in its own tenant and in another tenant. |
| T-12 Control canary | The RLS policy is dropped on purpose and the cross-tenant test must fail; the network policy is removed and the direct call must succeed. Shows that the tests can see a breach. |
| RLS on every table | Every table with `tenant_id` has RLS enabled and forced. |
| Direct access | A pod outside the Gateway cannot reach the app. |

The definitions are in [Testing](process/testing.md), section 4.

## 8. Where this is going

The first step has no operator: a registry, Argo CD, a library chart and Kyverno. The target state replaces the platform resources in your values with one resource of the platform's own:

```yaml
apiVersion: hamsp.io/v1alpha1
kind: HamspApp
metadata:
  name: my-app
spec:
  route:
    host: my-app.example.com
  database:
    name: my_app
    rls: required
  usage:
    events: [item.created]
  identity:
    roles: [admin, member]
  tenants:
    lifecycle: events
```

An operator reads it and creates the database and roles, the route, topics, network policies and the Zitadel project, and reports in its status what is ready. Your app stops seeing CNPG and Strimzi at all. The move needs no change in your app: the library chart starts rendering `HamspApp` instead. When the operator comes is in [ADR-0013](adr/0013-business-app-integration.md), item 9.
