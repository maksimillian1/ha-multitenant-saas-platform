# Tenant Management API

The HTTP API of Tenant Management (`apps/tenants`, ADR-0012, HAMSP-51). Its only caller today is the admin app's BFF; self-service sign-up (HAMSP-93) will be the second.

* [`openapi.yaml`](openapi.yaml): routes, schemas and status codes.
* [`examples/`](examples/): one JSON example per message. The Go tests of `apps/tenants/api` and of the admin app's client (`apps/admin/tenants`) check their messages against these examples (T-02).

Every route needs a Zitadel access token for the project `tenant-management` with the role `tenant-manager`. Every change also names the acting admin in `X-Admin-User-Id`, for the audit log.

Operations are synchronous: a create, retry, suspend, resume or delete answers when all its steps are done, or with the error of the step that failed and the tenant as the failure left it (`502`, `504` on a timeout, `409` when Zitadel or Kubernetes holds a foreign object with the tenant's name).
