# Prototype: local kind cluster

A local Kubernetes cluster that runs the walking skeleton (HAMSP-5) and the usage pipeline (HAMSP-26):

```
client → Cilium Gateway API (Envoy, routes by host)
       → API Gateway (Go, apps/gateway: JWT, x-tenant-id, routes by path)
       → ExampleApp (Go) → PgBouncer (CNPG Pooler, transaction mode) → PostgreSQL with RLS

API Gateway → UsageWriteAPI → Kafka (Strimzi) → Usage Writer → PgBouncer → usage cluster (PostgreSQL 18 + TimescaleDB)
```

Every API call through the API Gateway becomes one usage event ([contract](../docs/contracts/usage-events.md), [ADR-0005](../docs/adr/0005-kafka-on-strimzi.md), [ADR-0006](../docs/adr/0006-usage-store.md), [ADR-0007](../docs/adr/0007-usage-event-deduplication.md)). The usage database runs on its own CNPG cluster with TimescaleDB under the Timescale License ([ADR-0008](../docs/adr/0008-usage-store-on-timescaledb.md)).

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

`create-cluster.sh` recreates the kind cluster `cngp-demo`, installs the Gateway API CRDs, Cilium (with Gateway API) and ArgoCD, builds the `apps/exampleapp`, `apps/gateway` and `apps/usage` images and the TimescaleDB extension image (`images/timescaledb`) and loads them into kind, and points ArgoCD at the current branch (`GITOPS_REVISION` overrides it).

`bootstrap-tenants.sh` waits for Zitadel, creates the organizations `tenant-a` and `tenant-b` with one machine user each, and writes their client credentials to `prototype/.tenants.json` (git-ignored). It also sets the access token lifetime to 60 s for the expiry test.

It also creates the Zitadel project `platform-api`. Its ID is the audience every token must carry (HAMSP-22). Zitadel generates the ID, so the script writes it to the ConfigMap `gateway/api-gateway-audience` and restarts the API Gateway. Until the script has run, the API Gateway pods wait in `CreateContainerConfigError` and ArgoCD shows the `gateway` app as Degraded.

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

## ExampleApp API

| Request | Answer |
|---|---|
| `POST /items` `{"name": "..."}` | 201 and the item. A `tenant_id` in the body is ignored. |
| `GET /items` | 200 `{"items": [...]}` of the caller's tenant only |
| `GET /items/{id}` | 200, or 404 if it does not exist or belongs to another tenant |
| `PUT /items/{id}` `{"name": "..."}` | 200, or 404 as above |
| `DELETE /items/{id}` | 204, or 404 as above |

Headers between the Gateway and services: [docs/contracts/gateway-headers.md](../docs/contracts/gateway-headers.md).
