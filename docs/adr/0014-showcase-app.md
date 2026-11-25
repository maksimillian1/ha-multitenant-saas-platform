# ADR-0014: An open-source application as the platform's showcase business app

Date: 2026-11-11

## Status

Proposed (draft; the option is not chosen yet)

Drafted in HAMSP-96; the choice is made in HAMSP-103. The app is connected through the contract of [ADR-0013](0013-business-app-integration.md) and lives in its own repository (C-03).

## Context

ExampleApp (ADR-0001) is a minimal service for load and failure tests. It shows that the platform works, but not what it is for: a real product, run for many tenants, behind the platform's sign-in, on the shared PostgreSQL with RLS (ADR-0002).

We want a real open-source application, forked into this repository if needed, that:

* serves several tenants at once, the tenant taken only from `x-tenant-id` set by the Gateway (`docs/contracts/gateway-headers.md`);
* signs users in through the platform (Zitadel, ADR-0003) and has no sign-in of its own;
* keeps its data in the platform's PostgreSQL (CNPG), isolated by RLS with `app.current_tenant_id` set in every transaction;
* emits usage events (ADR-0010).

ExampleApp stays for the load and failure tests until this ADR says otherwise.

## Considered options

Facts below come from the projects' READMEs and docs, not from their code, and must be checked in the code before a decision.

| | Django-CRM (BottleCRM) | Fider | Kan | LearnHouse |
|---|---|---|---|---|
| Product | CRM | feedback board | kanban | LMS |
| Stack | Python (Django REST), SvelteKit | Go, React | TypeScript (Next.js, Drizzle) | Python (FastAPI, SQLModel, Alembic), Next.js, Node collab server |
| Extra services | to check | none | to check | Redis, Hocuspocus, pgvector, S3, Gemini (AI) |
| Tenants today | organizations, `org_id` | tenants, `tenant_id`, resolved by subdomain | workspaces | organizations; more than one only with the Enterprise license |
| RLS today | yes, `app.current_org` set by middleware | no | no | no (not mentioned) |
| Sign-in | own JWT | own cookie session, custom OAuth | to check | own JWT and refresh tokens; OIDC SSO only with the Enterprise license |
| License | MIT | AGPL-3.0 | to check | AGPL-3.0 core, separate Enterprise license |
| Fork work | sign-in and tenant source | tenant source, sign-in, RLS | tenancy check, sign-in, RLS | multi-org, sign-in, RLS, Redis/collab tenancy |

A later search found one more candidate, not yet compared in the table: **KANTOR** (`kana-consultant/kantor`, internal portal with kanban, HR and finance; Go and React; MIT). Its migrations already create RLS policies on `tenant_id` with `current_setting('app.current_tenant', true)`, the same shape as ADR-0002. Its sign-in and extra services are not checked yet.

The common work for every option (browser sign-in, tenant events, database roles, network policy, conformance tests) is the platform side of the contract in ADR-0013 and its sub-issues of HAMSP-95; it is not repeated here.

## Decision

Not taken. The owner's current preference is LearnHouse; its open questions are below.

### LearnHouse: open questions

* **Enterprise features.** Multi-org mode and OIDC SSO are Enterprise features. Building them ourselves in an AGPL fork must not use Enterprise-licensed code.
  * The Enterprise code is not in the public repository today: `apps/api/ee` and `apps/web/ee` are in `.gitignore` and overlaid from a private repository; `apps/api/src/core/ee_hooks.py` turns EE on only when `ee/hooks.py` exists.
  * Multi-org was a free, AGPL feature until early 2026 (`NEXT_PUBLIC_LEARNHOUSE_MULTI_ORG`). The last commit where it was free and switched only by that flag is `f50f20746d84d8277e9d7f3cbc397f19aabdf770` (2026-02-10). Organizations were resolved by subdomain in `apps/web/proxy.ts` and served under `apps/web/app/orgs/[orgslug]/`.
  * From 2026-01-05 to 2026-05-04 an `apps/api/ee` folder was public in the repository, with the root AGPL license as the only license file, while the README (from 2026-03-11) named a separate Enterprise license. Whether that code may be reused is a legal question; until it is answered, the fork does not use it.
  * No public fork re-implements multi-org or adds RLS. Some add OIDC sign-in (for example Keycloak per organization) and are prior art for the sign-in part only.
* **Tenant resolution.** Organizations are resolved by subdomain or custom domain, not by a header.
* **Sign-in.** Own JWT with refresh tokens; the collab server checks the same JWT. Both must accept the platform's identity.
* **Isolation.** The docs say every query is scoped to the organization; it is not known whether every table carries `org_id` directly. RLS is not used.
* **Async SQLModel and asyncpg.** `set_config(..., true)` must run inside the same transaction as the queries; with PgBouncer in transaction mode this holds only if every request runs in one explicit transaction.
* **Redis and collab state.** Cache keys, sessions, rate limits, job queues and Yjs documents are outside RLS and need a tenant prefix or a check.
* **Extra services.** Redis, Hocuspocus, pgvector, S3, Gemini: more to deploy and run in HA than the platform has today.
* **AGPL.** A modified version served over a network must offer its source.

## Consequences

To be written when an option is chosen.
