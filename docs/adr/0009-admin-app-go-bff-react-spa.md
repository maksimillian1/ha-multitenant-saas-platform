# ADR-0009: The admin app is a Go backend for frontend with an embedded React SPA

Date: 2026-10-29

## Status

Proposed

Raised in HAMSP-25, where the first super admin page was built. Keeps [ADR-0003](0003-auth-service-implementation.md) (Zitadel signs users in) and [ADR-0004](0004-api-gateway-go-service.md) (the API Gateway accepts only bearer tokens, so the admin app has its own public route).

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
2. **JSON API under `/api/`.** It is described in `apps/admin/openapi.yaml`. Every handler requires a session with the admin role (401 without a session, 403 without the role). Go types are generated with `oapi-codegen` (models only; handlers are written by hand in the style of the other services).
3. **The SPA is embedded.** It lives in `apps/admin/ui/app` and is built by Vite into `ui/app/dist`, which the `ui` package embeds with `go:embed`, as simple-rag's `ui` package embeds its templates. Go serves `index.html` for any route of the SPA to a signed-in user and redirects to the login without a session. The role is checked by the API alone: on 403 the SPA shows its access denied page with a sign-out button.

The SPA stack:

| Concern | Choice |
|---|---|
| Components | shadcn/ui (Radix, Tailwind CSS v4), copied into the repo, so we own the code |
| Server state | TanStack Query |
| Tables | TanStack Table, rendered with the shadcn/ui table (`components/data-table.tsx`) |
| Routing | TanStack Router |
| Forms | react-hook-form with zod schemas, through the shadcn/ui field components; added with the first form |
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

**Bad / risks:**

* The image build needs Node (a build stage in the Dockerfile), and CI gets an SPA job and an npm license check (C-03).
* Platform staff with a role other than `super-admin` can download the SPA, which shows them every screen the app has. The data stays behind the API.
* Hand-written handlers can still drift from `openapi.yaml`; only the types are generated. If it starts to happen, generate the server interface (`std-http-server`) too.
* The CSP allows inline styles, because Radix UI injects `<style>` elements. Scripts are limited to `'self'`.
* Session cookies are not `Secure` while the prototype runs on plain HTTP. In EKS (TLS) they must be `Secure`, and state-changing API calls need CSRF protection beyond `SameSite=Lax` (for example a required custom header) before the first mutation is added.
* A role revoked in Zitadel takes effect only at the next sign-in (the session lasts an hour).
