# Gateway → service headers

The contract between the API Gateway (`apps/gateway`, [ADR-0004](../adr/0004-api-gateway-go-service.md)) and the services behind it (ExampleApp, Tenant Management, Metering Query Service). Introduced by HAMSP-5.

## `x-tenant-id`

| | |
|---|---|
| Value | Zitadel Organization ID of the caller, taken from the claim `urn:zitadel:iam:user:resourceowner:id` of the validated JWT (ADR-0003). |
| Set by | The API Gateway only. |
| Client value | Always dropped. The Gateway removes any `x-tenant-id` sent by the client before it validates the token, then sets the header from the claim. |
| `Authorization` | Removed by the Gateway; services never see the token. |
| Missing | The Gateway answers 401 when the token is missing, invalid or not issued for the platform API (its `aud` lacks the platform project ID); such requests never reach a service. If a service still gets a request without the header, it answers 401. |

Rules for services:

* Take the tenant only from `x-tenant-id`. Never from the body, the path or the query.
* Do not validate the JWT again; the Gateway does not forward it.
* Put the tenant into every database transaction with `set_config('app.current_tenant_id', <tenant>, true)` (the same as `SET LOCAL`), so RLS limits every query to that tenant (ADR-0002).
* Answer 404, not 403, for a record of another tenant, so record IDs of other tenants cannot be probed (QS-01).

The header is safe to trust only because a `CiliumNetworkPolicy` lets nothing but the Gateway's pods (`app.kubernetes.io/name: api-gateway` in namespace `gateway`) reach the service. Every service that trusts `x-tenant-id` needs such a policy.
