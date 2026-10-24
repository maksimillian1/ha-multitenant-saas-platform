# ADR-0004: API Gateway as a Go service behind Cilium Gateway API

Date: 2026-10-24

## Status

Proposed

Amends [ADR-0001](0001-system-boundaries.md), item 1: the API Gateway is two layers, not one Envoy proxy. Input for HAMSP-17, which decides how the entry point runs in EKS.

## Context

ADR-0001 makes Envoy the API Gateway: it validates the JWT, takes `tenant_id`, enforces rate limits and reports usage (FR-03, FR-05, FR-07). The walking skeleton (HAMSP-5) showed the limits of that on our stack:

* Cilium 1.19 runs the Kubernetes Gateway API on its built-in Envoy, but the generated listener cannot take extra HTTP filters such as `jwt_authn`. `CiliumGatewayClassConfig` only configures the Service and access logs.
* The first skeleton iteration worked around it with a hand-written `CiliumEnvoyConfig` listener (`jwt_authn`, header mutation). It worked, but every new rule is raw Envoy xDS in YAML, with no tests and errors visible only in Cilium agent logs.
* The Gateway will get more logic than Envoy filters express well: per-tenant routing, quotas from our own store (FR-07, QS-15), usage events with retries (FR-05), later possibly API composition. Forking Envoy is not an option.

## Decision

The entry point has two layers:

1. **Infrastructure entry: Cilium Gateway API** (Gateway + HTTPRoute on Cilium's Envoy). It terminates the connection (TLS in EKS, behind the NLB), routes by host, and holds no business logic. Zitadel's host is routed straight to Zitadel, because its APIs do their own authentication.
2. **API Gateway: a Go service** (`apps/gateway`). For every request on the API host it:
   * drops any `x-tenant-id` sent by the client;
   * validates the Zitadel access token locally against the cached JWKS (`zitadel/oidc`, allowed by ADR-0003): issuer, signature, expiry;
   * sets `x-tenant-id` from `urn:zitadel:iam:user:resourceowner:id` and removes the token;
   * routes by path prefix to the owning service.

   Rate limits, quotas and usage reporting (FR-05, FR-07) will be added here.

Services trust `x-tenant-id` only because a `CiliumNetworkPolicy` lets nothing but the API Gateway's pods reach them ([contract](../contracts/gateway-headers.md)).

## Considered options

| Option | Why not (for now) |
|---|---|
| `CiliumEnvoyConfig` with `jwt_authn` (HAMSP-5, first iteration) | Works, but all logic is untested Envoy YAML; quotas and usage would need extra Envoy services anyway. |
| Gateway API + ExternalAuth (Cilium 1.20) with a Go ext_authz service | Keeps Envoy as the only data path, but splits the logic between Envoy config and a service, and needs a Cilium upgrade. The best fallback if the Go data path does not meet the load targets. |
| Envoy Gateway (SecurityPolicy with JWT) | A second Envoy control plane next to Cilium; logic still in Envoy config. |

## Consequences

**Good:**

* All request logic is Go code with unit tests (`apps/gateway`), in the same language as the services.
* The infrastructure layer stays standard Gateway API, so the EKS setup (NLB, TLS) is configuration, not code.
* Zitadel key rotation needs no restart: an unknown key ID makes the Gateway refetch the JWKS.

**Bad / risks:**

* **Our code is in the hot path of every request**, with one extra hop. It must hold 20 000 RPS with low added latency and survive rolling deploys and Spot interruptions (QS-03, QS-05, QS-10). It needs at least 2 replicas, a PodDisruptionBudget and a k6 test before this ADR is accepted (HAMSP-17).
* Proxy features Envoy gives for free (timeouts, retries, streaming, WSS) are now `net/http/httputil.ReverseProxy` behaviour and must be tested.
* A token with an unknown key ID triggers a JWKS fetch; a flood of forged tokens becomes a flood of requests to Zitadel. The refetch needs a rate limit before production.
* The token audience is not checked yet (HAMSP-22).
* When accepted, `docs/architecture/c4/workspace.dsl` must show the two layers instead of one "Envoy Proxy" API Gateway.
