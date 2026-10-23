# Prototype: local kind cluster

A local Kubernetes cluster that runs the walking skeleton (HAMSP-5):

```
client → API Gateway (Cilium Envoy: jwt_authn, x-tenant-id) → ExampleApp (Go)
       → PgBouncer (CNPG Pooler, transaction mode) → PostgreSQL with RLS
```

Zitadel issues the tokens. Everything except Cilium and ArgoCD is deployed by ArgoCD from `k8s/apps/*` (one Application per directory, namespace = directory name).

| Directory | What |
|---|---|
| `k8s/apps/cnpg-system` | CloudNativePG operator |
| `k8s/apps/minio` | S3 for Barman backups |
| `k8s/apps/database` | PostgreSQL cluster, roles, PgBouncer |
| `k8s/apps/zitadel` | Zitadel with its own database in the same cluster |
| `k8s/apps/exampleapp` | ExampleApp, its migration job and network policy |
| `k8s/apps/gateway` | API Gateway: a `CiliumEnvoyConfig` on Cilium's Envoy, NodePort 30080 |

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

`create-cluster.sh` recreates the kind cluster `cngp-demo`, installs Cilium (with `CiliumEnvoyConfig`) and ArgoCD, builds the ExampleApp image and loads it into kind, and points ArgoCD at the current branch (`GITOPS_REVISION` overrides it).

`bootstrap-tenants.sh` waits for Zitadel, creates the organizations `tenant-a` and `tenant-b` with one machine user each, and writes their client credentials to `prototype/.tenants.json` (git-ignored). It also sets the access token lifetime to 60 s for the expiry test.

The e2e tests take about two minutes; `-short` skips the token expiry test.

## Try it by hand

```bash
TENANT=$(jq -r '.tenants[0] | "\(.client_id):\(.client_secret)"' prototype/.tenants.json)
TOKEN=$(curl -s --connect-to zitadel.localtest.me:30080:127.0.0.1:30080 -u "$TENANT" \
  -d 'grant_type=client_credentials&scope=openid urn:zitadel:iam:user:resourceowner' \
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
