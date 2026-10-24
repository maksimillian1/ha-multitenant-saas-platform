# Prototype: local kind cluster

A local Kubernetes cluster that runs the walking skeleton (HAMSP-5):

```
client → Cilium Gateway API (Envoy, routes by host)
       → API Gateway (Go, apps/gateway: JWT, x-tenant-id, routes by path)
       → ExampleApp (Go) → PgBouncer (CNPG Pooler, transaction mode) → PostgreSQL with RLS
```

The two gateway layers are described in [ADR-0004](../docs/adr/0004-api-gateway-go-service.md).

Zitadel issues the tokens. Everything except Cilium and ArgoCD is deployed by ArgoCD from `k8s/apps/*` (one Application per directory, namespace = directory name).

| Directory | What |
|---|---|
| `k8s/apps/cnpg-system` | CloudNativePG operator |
| `k8s/apps/minio` | S3 for Barman backups |
| `k8s/apps/database` | PostgreSQL cluster, roles, PgBouncer |
| `k8s/apps/zitadel` | Zitadel with its own database in the same cluster |
| `k8s/apps/exampleapp` | ExampleApp, its migration job and network policy |
| `k8s/apps/gateway` | Public Gateway (Cilium Gateway API, host network port 30080) and the API Gateway service |

Secrets in these manifests are for this local cluster only.

## Run

Needs Docker (about 6 GB of memory for the cluster), kind, kubectl, helm, jq and Go.
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

`create-cluster.sh` recreates the kind cluster `cngp-demo`, installs the Gateway API CRDs, Cilium (with Gateway API) and ArgoCD, builds the `apps/exampleapp` and `apps/gateway` images and loads them into kind, and points ArgoCD at the current branch (`GITOPS_REVISION` overrides it).

`bootstrap-tenants.sh` waits for Zitadel, creates the organizations `tenant-a` and `tenant-b` with one machine user each, and writes their client credentials to `prototype/.tenants.json` (git-ignored). It also sets the access token lifetime to 60 s for the expiry test.

It also creates the Zitadel project `platform-api`. Its ID is the audience every token must carry (HAMSP-22). Zitadel generates the ID, so the script writes it to the ConfigMap `gateway/api-gateway-audience` and restarts the API Gateway. Until the script has run, the API Gateway pods wait in `CreateContainerConfigError` and ArgoCD shows the `gateway` app as Degraded.

The e2e tests take about two minutes; `-short` skips the token expiry test.

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

## ExampleApp API

| Request | Answer |
|---|---|
| `POST /items` `{"name": "..."}` | 201 and the item. A `tenant_id` in the body is ignored. |
| `GET /items` | 200 `{"items": [...]}` of the caller's tenant only |
| `GET /items/{id}` | 200, or 404 if it does not exist or belongs to another tenant |
| `PUT /items/{id}` `{"name": "..."}` | 200, or 404 as above |
| `DELETE /items/{id}` | 204, or 404 as above |

Headers between the Gateway and services: [docs/contracts/gateway-headers.md](../docs/contracts/gateway-headers.md).
