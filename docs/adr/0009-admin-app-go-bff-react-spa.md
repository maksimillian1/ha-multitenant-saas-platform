# ADR-0009: The admin app is a Go backend for frontend with an embedded React SPA

Date: 2026-10-29

## Status

Proposed

Raised in HAMSP-25, where the first super admin page was built. Keeps [ADR-0003](0003-auth-service-implementation.md) (Zitadel signs users in) and [ADR-0004](0004-api-gateway-go-service.md) (the API Gateway accepts only bearer tokens, so the admin app has its own public route). Item 4 and the CSRF protection were added in HAMSP-34, with the first pages that change data in Zitadel. Item 5 was added in HAMSP-37; HAMSP-36 replaced the personal access tokens of items 4 and 5 with a short-lived key and merged the two service accounts into one.

## Context

The platform needs a super admin UI: tenants, users, plans, quotas, and commands such as blocking a tenant or rotating a secret. The first version (`apps/admin`) was one Go service that signed the user in with Zitadel and rendered one `html/template` page. That proved the sign-in and the role check, but templates with hand-written CSS do not scale to tables, forms and dialogs, and they look dated.

What we want:

* a modern, clean UI built from ready components;
* as few services as possible: no separate admin API service next to a separate UI service;
* tokens never reach the browser;
* one source of truth for the API types, checked in CI.

## Decision

`apps/admin` stays **one Go service and one image**. It is a backend for frontend (BFF):

1. **Sign-in on the server.** Go does the OIDC authorization code flow with PKCE against Zitadel (`zitadel/oidc`) and keeps only an encrypted, HttpOnly session cookie in the browser. The role comes from the ID token's project roles claim. The Zitadel project `platform-admin` checks roles on sign-in (`projectRoleCheck`), so a user without any role in it, such as a tenant's user, gets no session at all.
2. **JSON API under `/api/`.** It is described in `apps/admin/openapi.yaml`. Every handler requires a session with the admin role (401 without a session, 403 without the role). State-changing requests (POST, PUT, PATCH, DELETE) also need the header `X-Requested-With: XMLHttpRequest`, which a cross-site page cannot send without a CORS preflight that the server never allows, and pass Go's `http.CrossOriginProtection` (`Sec-Fetch-Site`, `Origin`); otherwise 403. Go types are generated with `oapi-codegen` (models only; handlers are written by hand in the style of the other services), and a test checks that every route in the spec has a handler.
3. **The SPA is embedded.** It lives in `apps/admin/ui/app` and is built by Vite into `ui/app/dist`, which the `ui` package embeds with `go:embed`, as simple-rag's `ui` package embeds its templates. Go serves `index.html` for any route of the SPA to a signed-in user and redirects to the login without a session. The role is checked by the API alone: on 403 the SPA shows its access denied page with a sign-out button.
4. **Zitadel is managed by the app's own service account.** The API reads and changes Zitadel's organizations, users, role assignments (authorizations) and projects through Zitadel's v2 services, called over Connect with JSON by a small hand-written client (`apps/admin/zitadel`; no SDK and nothing from `github.com/zitadel/zitadel`, ADR-0003). It calls them as the machine user `admin-app` with the instance roles `IAM_ORG_MANAGER` and `IAM_BRANDING_MANAGER` (item 5), not with the signed-in admin's token. The app holds a JWT profile key of that user, mounted as a file (`ZITADEL_KEY_FILE`), and gets access tokens with the JWT bearer grant (RFC 7523); a token lives 60 seconds on the prototype and is renewed before it expires. `bootstrap-tenants.sh` creates a new key valid for 30 days on every run and deletes the old keys and any personal access token. The app checks the credentials of `admin-app` and of the IAM admin `iam-admin` every 5 minutes and logs an `ERROR` event `service_account_credential_drift` for a personal access token, a key it does not use, a second key or a key valid for more than 30 days. The admin's own token is not used: the admin app keeps one role system (`super-admin` in `platform-admin`) and the session cookie stays small. Every change is logged with the admin's user ID, the route, the target and the status. The API refuses (409) to lock, deactivate or delete the signed-in admin or the service account, to deactivate or delete their organizations, and to change the admin's own role assignments, so an admin cannot lock out themselves or the app.
5. **Login branding uses a custom instance role (HAMSP-37, HAMSP-36).** Branding is Zitadel's label policy, which v4.19.2 changes only through the v1 REST gateway and the assets API. An organization's branding goes through `admin-app` like everything else. The instance's branding (the platform default, which the admin app also takes its own primary color, logo and icon from) needs `iam.policy.write`, which among Zitadel's built-in roles only `IAM_OWNER` has. Rather than give any account of the app `IAM_OWNER`, the Zitadel configuration (`InternalAuthZ.RolePermissionMappings` in `prototype/k8s/apps/zitadel/values.yaml`) defines the role `IAM_BRANDING_MANAGER` with only `iam.policy.read`, `iam.policy.write` and `iam.policy.delete`, and `admin-app` holds it next to `IAM_ORG_MANAGER`. The configured list replaces Zitadel's defaults, so it is a copy of them plus this role, refreshed by hand when the chart is upgraded. (HAMSP-37 first used a second machine user `admin-app-instance` with `IAM_OWNER`; HAMSP-36 removed it.) The BFF serves the assets from its own origin, so the CSP stays `img-src 'self'`; it types them by content, sends `nosniff`, and puts SVGs under `Content-Security-Policy: sandbox`.

The SPA stack:

| Concern | Choice |
|---|---|
| Components | shadcn/ui (Radix, Tailwind CSS v4), copied into the repo, so we own the code |
| Server state | TanStack Query |
| Tables | TanStack Table, rendered with the shadcn/ui table (`components/data-table.tsx`) |
| Routing | TanStack Router |
| Forms | react-hook-form with zod schemas, through the shadcn/ui field components |
| Tests | Vitest with Testing Library in jsdom, rendering the real router against a stubbed `fetch` |
| API client | `openapi-fetch` with types from `openapi-typescript`, generated from the same `openapi.yaml` |
| Build | Vite, TypeScript, oxlint |

## Considered options

| Option | Why not |
|---|---|
| Go `html/template` pages (the first version) | No component library; tables, forms and dialogs by hand. |
| Go + htmx + templ | Fewer moving parts, but no mature shadcn-like component set; complex tables and forms still mean hand-written HTML and JS. |
| Refine with its shadcn/ui integration | Saves code on many CRUD screens, but its hooks and data provider run through every page, v4 to v5 was a heavy migration, development slowed in 2026, and super admin actions are mostly commands, not CRUD. TanStack is what Refine is built on anyway. |
| Separate admin API service and a static UI (S3/CDN or nginx) | Two services and two deployments for one internal app; CORS or a proxy; the token has to live in the browser or in yet another BFF. |
| Next.js (SSR) as the BFF | A Node service in production next to Go services, with its own auth library and runtime to patch. |

## Consequences

**Good:**

* One image, one Deployment, one route. The SPA and the API cannot drift in deployment.
* Tokens stay on the server; the browser has only an HttpOnly cookie, so an XSS in the SPA cannot steal a token.
* The API types on both sides come from `openapi.yaml`; CI fails when either generated file is out of date.
* shadcn/ui code is ours: no UI library upgrade can break the pages.
* The SPA's files are served only to users with a role in `platform-admin`, so the admin UI is not public.
* Platform admins manage tenants' organizations, users and role assignments without Zitadel's console and without a Zitadel role of their own.

**Bad / risks:**

* The image build needs Node (a build stage in the Dockerfile), and CI gets an SPA job and an npm license check (C-03).
* Platform staff with a role other than `super-admin` can download the SPA, which shows them every screen the app has. The data stays behind the API.
* Hand-written handlers can still drift from `openapi.yaml`; only the types are generated. If it starts to happen, generate the server interface (`std-http-server`) too.
* The CSP allows inline styles, because Radix UI injects `<style>` elements. Scripts are limited to `'self'`.
* Session cookies are not `Secure` while the prototype runs on plain HTTP. In EKS (TLS) they must be `Secure`. State-changing API calls have CSRF protection beyond `SameSite=Lax` since HAMSP-34 (item 2).
* Zitadel's audit trail shows the service account, not the admin, as the editor of every change; only the admin app's log says which admin it was.
* The service account can change every organization's users and role assignments and the instance's policies. Its key is in a Kubernetes Secret on the prototype; anyone who reads the Secret has that power until the key expires (at most 30 days) or is deleted in Zitadel, and a stolen access token works for at most its lifetime (60 seconds on the prototype). Where the key is kept beyond the prototype is the secret-management decision (HAMSP-92). The credential drift event has no alert yet (HAMSP-89).
* Deleting an organization deletes it in Zitadel only; the tenant's data in other stores stays until the tenant lifecycle (FR-01, QS-18) exists.
* A role revoked in Zitadel takes effect only at the next sign-in (the session lasts an hour).
