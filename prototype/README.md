# Prototype: local kind cluster

A local Kubernetes cluster that runs the walking skeleton (HAMSP-5) and the usage pipeline (HAMSP-26):

```
client → Cilium Gateway API (Envoy, routes by host)
       → API Gateway (Go, apps/gateway: JWT, x-tenant-id, routes by path)
       → ExampleApp (Go) → PgBouncer (CNPG Pooler, transaction mode) → PostgreSQL with RLS

API Gateway → UsageWriteAPI → Kafka (Strimzi) → Usage Writer → PgBouncer → usage cluster (PostgreSQL 18 + TimescaleDB)
```

Every API call through the API Gateway becomes one usage event ([contract](../docs/contracts/usage-event/README.md), [ADR-0005](../docs/adr/0005-kafka-on-strimzi.md), [ADR-0006](../docs/adr/0006-usage-store.md), [ADR-0007](../docs/adr/0007-usage-event-deduplication.md)). The usage database runs on its own CNPG cluster with TimescaleDB under the Timescale License ([ADR-0008](../docs/adr/0008-usage-store-on-timescaledb.md)).

The two gateway layers are described in [ADR-0004](../docs/adr/0004-api-gateway-go-service.md).

Zitadel issues the tokens. Everything except Cilium and ArgoCD is deployed by ArgoCD from `k8s/apps/*` (one Application per directory, namespace = directory name).

| Directory | What |
|---|---|
| `k8s/apps/cnpg-system` | CloudNativePG operator |
| `k8s/apps/minio` | S3 for Barman backups |
| `k8s/apps/database` | Main PostgreSQL cluster, roles, PgBouncer |
| `k8s/apps/usage-db` | Usage cluster: PostgreSQL 18 with TimescaleDB (ImageVolume), the `usage` database, roles, PgBouncer |
| `k8s/apps/zitadel` | Zitadel with its own database in the same cluster |
| `k8s/apps/exampleapp` | ExampleApp, its migration job and network policy |
| `k8s/apps/gateway` | Public Gateway (Cilium Gateway API, host network port 30080) and the API Gateway service |
| `k8s/apps/kafka` | Strimzi operator, a Kafka cluster of 3 dual-role KRaft nodes and the `usage-events` topic |
| `k8s/apps/usage` | UsageWriteAPI, Usage Writer and the job that migrates the `usage` database |
| `k8s/apps/admin` | Super admin app (Go BFF with a React SPA, ADR-0009) behind a Zitadel browser sign-in, on `admin.localtest.me`; it manages Zitadel's organizations, users and role assignments |

Secrets in these manifests are for this local cluster only.

## Run

Needs Docker (about 8 GB of memory for the cluster; Kafka takes about 2 GB of it), kind, kubectl, helm, jq and Go.
ArgoCD pulls the manifests from GitHub, so push your branch first.

```bash
./prototype/create-cluster.sh
```

```bash
./prototype/bootstrap-tenants.sh
```

```bash
go test -C prototype/e2e -count=1 -v ./...
```

`create-cluster.sh` recreates the kind cluster `cngp-demo`, installs the Gateway API CRDs, Cilium (with Gateway API) and ArgoCD, builds the `apps/exampleapp`, `apps/gateway`, `apps/usage` and `apps/admin` images and loads them into kind (the usage cluster pulls the TimescaleDB extension image from GHCR, `images/timescaledb`), and points ArgoCD at the current branch (`GITOPS_REVISION` overrides it).

`bootstrap-tenants.sh` waits for Zitadel, creates the organizations `tenant-a` and `tenant-b` with one machine user each, and writes their client credentials to `prototype/.tenants.json` (git-ignored). It also sets the access token lifetime to 60 s for the expiry test.

It also creates the Zitadel project `platform-api`. Its ID is the audience every token must carry (HAMSP-22). Zitadel gives that audience to a machine user of any organization, so the API Gateway also serves only the organizations on its tenant list (HAMSP-24). The script writes the project ID to the ConfigMap `gateway/api-gateway-audience` and the tenants' organization IDs, separated by commas, to `gateway/api-gateway-tenants`, and restarts the API Gateway when either changed. Until the script has run, the API Gateway pods wait in `CreateContainerConfigError` and ArgoCD shows the `gateway` app as Degraded.

Two more machine users are not tenants: `outsider-client` in the organization `outsider` and `iam-admin-org-client` in the IAM admin's organization. Their credentials are under `non_tenants` in `.tenants.json`; the e2e tests check that their tokens get 401.

The e2e tests take about ten minutes, most of it the usage outage tests (UsageWriteAPI unreachable for 60 s, the usage database unreachable for 5 minutes, a Kafka broker deleted). `-short` skips them and the token expiry test. The outage tests add temporary `CiliumNetworkPolicy` deny rules and remove them at the end.

Usage events, as the superuser:

```bash
kubectl -n usage-db exec usage-pg-cluster-1 -c postgres -- psql -U postgres -d usage \
  -c "SELECT tenant_id, attributes->>'path', attributes->>'status', event_time FROM usage_events ORDER BY event_time DESC LIMIT 10"
```

The API Gateway's usage counters (sent, dropped, failed attempts) are on its metrics port 9090: `kubectl -n gateway port-forward deploy/api-gateway 9090` and `curl localhost:9090/metrics`.

The CNPG operator is 1.30. A cluster created with CNPG 1.23 (before HAMSP-25) cannot jump to 1.30 directly: 1.30 rejects the plain-HTTP status port of instances created before 1.24. Upgrade it through 1.29.1 first and let the instances roll (commit `62b33ae`), then to 1.30. On such a cluster the old `usage` database and roles in `main-pg-cluster` are dropped by the `legacy-usage` Database resource and `ensure: absent` roles once PgBouncer closes its idle server connections to them (`server_idle_timeout`, 10 minutes). CNPG retries the roles only on its next reconcile of `main-pg-cluster` (any change to the Cluster, a switchover, or the periodic resync), so they can outlive the database for a while.

Known issue: in host network mode Cilium 1.19 never sets an address on the Gateway, so it stays `Programmed=False` (`AddressNotAssigned`) and ArgoCD shows the `gateway` app as Progressing. Traffic on port 30080 works.

## Try it by hand

```bash
TENANT=$(jq -r '.tenants[0] | "\(.client_id):\(.client_secret)"' prototype/.tenants.json)
AUD=$(jq -r .audience prototype/.tenants.json)
TOKEN=$(curl -s --connect-to zitadel.localtest.me:30080:127.0.0.1:30080 -u "$TENANT" \
  -d "grant_type=client_credentials&scope=openid urn:zitadel:iam:user:resourceowner urn:zitadel:iam:org:project:id:${AUD}:aud" \
  http://zitadel.localtest.me:30080/oauth/v2/token | jq -r .access_token)
curl -s --connect-to api.localtest.me:30080:127.0.0.1:30080 -H "Authorization: Bearer $TOKEN" \
  -d '{"name":"hello"}' http://api.localtest.me:30080/items
```

`*.localtest.me` resolves to 127.0.0.1 in public DNS; `--connect-to` makes it work without DNS too.

The same in a browser: `go run -C prototype/authdemo .` and open http://127.0.0.1:8090. The page gets tokens for both tenants, shows their claims, calls the API with a valid, missing or tampered token and runs the auth and isolation checks in one click. It reads the client secrets from `.tenants.json` and proxies to port 30080, so it is for this local cluster only.

## Super admin app

`apps/admin` serves a React SPA at http://admin.localtest.me:30080 (ADR-0009). Without a session it redirects to Zitadel's login. Zitadel lets in only users with a role in the project `platform-admin` (`projectRoleCheck`). The API requires the `super-admin` role, taken from the ID token; a user without it gets the access denied page. The session is an encrypted cookie that lasts an hour; the tokens stay on the server.

The pages manage what lives in Zitadel (HAMSP-34):

| Page | What |
|---|---|
| Overview | Counts of organizations, human and machine users and projects; the admin's session and roles |
| Organizations | Search, filter by state; create, rename, deactivate, activate and delete an organization; its users and projects |
| Users | Users of every organization: search, filter by organization, type and state; create humans and machine users; edit, set a password, lock, deactivate, delete; generate a machine user's client secret; add, change and remove role assignments |
| Projects | Read only: projects, their roles and who holds them |

Deleting an organization deletes it in Zitadel only: the tenant's rows in PostgreSQL and its usage events stay (tenant lifecycle, HAMSP-6). The app does not let the admin lock, deactivate or delete their own user or the service account, deactivate or delete their organizations, or change their own role assignments.

The app calls Zitadel's v2 APIs (Connect with JSON) as its own service account `admin-app`, a machine user with the instance role `IAM_ORG_MANAGER`, never with the admin's token. Zitadel's audit trail therefore shows `admin-app` as the editor; the app's log has a line `admin change` with the admin's user ID, the route, the target and the status for every change. State-changing requests need the header `X-Requested-With: XMLHttpRequest` and must come from the app's origin (CSRF).

`bootstrap-tenants.sh` sets it up. It switches the instance to Zitadel's built-in login (the instance requires Login V2, which this cluster does not deploy), creates the project, the role and an OIDC web app, writes the app's client and a session key to the Secret `admin/admin-oidc`, creates the service accounts `admin-app` and `admin-app-instance` with personal access tokens (new on every run, valid for a year) in the Secret `admin/admin-zitadel`, and creates three human users in the IAM admin's organization: `super-admin` with the role (all pages), `viewer` with the role `viewer` only (access denied page) and `regular-user` with no role (Zitadel refuses the sign-in). Their passwords are new on every run:

```bash
jq -r '.admin.users[] | "\(.username)  \(.password)  roles: \(.roles)"' prototype/.tenants.json
```

Zitadel may offer to set up a second factor after the password; skip it.

The Zitadel client has an integration test that runs against this cluster with the service account's token. It creates and deletes an organization with a human and a machine user, and assigns and revokes a role in `platform-admin`:

```bash
ZITADEL_TEST_URL=http://zitadel.localtest.me:30080 \
ZITADEL_TEST_TOKEN=$(kubectl -n admin get secret admin-zitadel -o jsonpath='{.data.ZITADEL_TOKEN}' | base64 -d) \
go test -C apps/admin -count=1 -run TestIntegration -v ./zitadel/
```

### Login branding

The Branding tab of an organization and the Platform branding page edit Zitadel's label policies (HAMSP-37): colors for light and dark, theme mode, two flags, and the logo, icon and font. Changes go to a draft, which a preview shows; **Publish** makes the Zitadel login show it. An organization without its own branding shows the platform's, and **Reset to platform branding** removes its own. The admin app takes its primary color, logo and icon from the published platform branding.

The platform branding needs the instance role `IAM_OWNER`, so `bootstrap-tenants.sh` creates a second machine user, `admin-app-instance`, and writes its token as `ZITADEL_INSTANCE_TOKEN` to the same Secret; the app uses it for the platform branding only. The app does not start without it. The integration test publishes branding for `tenant-a` and checks the login's CSS for `tenant-a`, `tenant-b` and the platform; it restores what it changed:

```bash
ZITADEL_TEST_URL=http://zitadel.localtest.me:30080 \
ZITADEL_TEST_TOKEN=$(kubectl -n admin get secret admin-zitadel -o jsonpath='{.data.ZITADEL_TOKEN}' | base64 -d) \
ZITADEL_TEST_INSTANCE_TOKEN=$(kubectl -n admin get secret admin-zitadel -o jsonpath='{.data.ZITADEL_INSTANCE_TOKEN}' | base64 -d) \
go test -C apps/admin -count=1 -run TestBrandingIntegration -v ./zitadel/
```

The SPA lives in `apps/admin/ui/app` (Node 22, `.nvmrc`); `npm test` runs its tests (Vitest, jsdom), which render the real pages against a stubbed API. `npm run dev` proxies `/api` and `/auth` to a Go server on `localhost:8080`; signing in through it needs `PUBLIC_URL=http://localhost:5173` and that callback URL added to the Zitadel app, which the bootstrap does not do. The API is described in `apps/admin/openapi.yaml`: after changing it, run `go generate ./...` in `apps/admin` and `npm run gen:api` in `apps/admin/ui/app`. CI fails when the generated types are out of date, and a Go test fails when a route and the spec disagree.

## ExampleApp API

| Request | Answer |
|---|---|
| `POST /items` `{"name": "..."}` | 201 and the item. A `tenant_id` in the body is ignored. |
| `GET /items` | 200 `{"items": [...]}` of the caller's tenant only |
| `GET /items/{id}` | 200, or 404 if it does not exist or belongs to another tenant |
| `PUT /items/{id}` `{"name": "..."}` | 200, or 404 as above |
| `DELETE /items/{id}` | 204, or 404 as above |

Headers between the Gateway and services: [docs/contracts/gateway-headers.md](../docs/contracts/gateway-headers.md).
