# Networking & Security

Cilium enforces network isolation with CiliumNetworkPolicy (ADR-0001, item 1). A pod that no policy selects is open; a policy that selects a pod denies everything it does not allow, in the direction it lists.

## North-south traffic

Browsers and API clients enter through the Cilium Gateway API (`public` Gateway, NodePort 30080 on kind). Tenant API calls go through the API Gateway, which validates the JWT and sets `x-tenant-id` (ADR-0004). The admin app, the tenant web page (exampleweb) and Zitadel have their own routes, because they sign users in themselves.

## Platform services

| Service | Who may connect | Policy |
|---|---|---|
| ExampleApp | API Gateway only: ExampleApp trusts `x-tenant-id` | `prototype/k8s/apps/exampleapp/network-policy.yaml` |
| UsageWriteAPI, UsageReadAPI | their callers only | `prototype/k8s/apps/usage/network-policy.yaml` |
| Tenant Management | admin app pods only (namespace `admin`, label `app.kubernetes.io/name=admin`) | `prototype/k8s/apps/tenants/network-policy.yaml` |

## Zitadel signing keys (QS-11)

The API Gateway checks tokens itself with Zitadel's public keys (JWKS). At start it fetches them from Zitadel; when Zitadel does not answer, it reads the last known set from the ConfigMap `gateway/zitadel-jwks` if that is at most 24 h old, and is not Ready without keys. The CronJob `gateway/jwks-sync` writes the ConfigMap every minute and is the only ServiceAccount that may update it; the gateway mounts it read-only. A key that only the cache has is dropped as soon as Zitadel answers. The JWKS is public, so it is not a secret; the risk is a planted key, which the narrow write access covers.

## Tenant namespaces (QS-02)

Tenant Management creates `tenant-<tenant_id>` for every tenant (ADR-0012), labelled `hamsp.io/tenant-id`, with a ResourceQuota and the CiliumNetworkPolicy `tenant-isolation` that selects every pod in it:

* ingress only from pods of the same namespace;
* egress only to pods of the same namespace, to kube-dns on port 53 and to `world` (outside the cluster).

So a pod of tenant A cannot connect to a pod of tenant B, nor to platform services, and no pod outside the namespace can connect in. Agent workloads (FR-04) will run here; how they reach UsageWriteAPI is decided with HAMSP-18 and needs an egress rule then.

Tested on kind by `prototype/e2e/tenant_network_test.go:TestTenantNamespacesAreIsolated`, which provisions two tenants through Tenant Management; its same-namespace probes are the control.
