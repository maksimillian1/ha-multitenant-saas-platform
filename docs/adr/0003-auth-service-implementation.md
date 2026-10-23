# ADR-0003: Auth Service Implementation (Zitadel)

Date: 2026-10-22

## Status

Proposed

Amends [ADR-0001](0001-system-boundaries.md), item 1: Zitadel replaces Cognito/Auth0 as the token issuer.

## Context

We need one service that logs users in, issues tokens and knows which tenant each user belongs to. It must:

* support many tenants: every B2B customer has its own users, roles, SSO and login branding;
* speak standard OIDC/OAuth2, so the Gateway and our services only need to validate JWTs;
* fit our Go backend;
* run inside our EKS cluster on our existing PostgreSQL (CNPG).

We compared two self-hosted options: **Ory Hydra** and **Zitadel**.

## Decision

We use **Zitadel** (self-hosted) as the Auth Service.

**Why not Hydra:**

* Hydra is only an OAuth2/OIDC token server. We would have to build users, login pages and roles ourselves, or add Ory Kratos + Keto. That means three products to run and glue together.
* Hydra has no built-in tenant model, so we would have to build multi-tenancy on top of it.

**Why Zitadel:**

* **Native multi-tenancy:** one Zitadel Organization = one tenant, with its own users, roles, SSO (SAML/OIDC) and branding.
* Users, MFA, passkeys, roles and audit log work out of the box.
* Written in Go, with official Go libraries (`zitadel-go`, `zitadel/oidc`) that give us ready middleware for our APIs.
* Uses PostgreSQL, so it fits our CNPG setup.

**How we run it:**

1. Own namespace, official image, **no code changes**. We customise only through config, Actions v2 (webhooks) and the login app.
2. Own database in the CNPG cluster, separate from application data.
3. `tenant_id` = Zitadel Organization ID taken from the token. The Gateway validates the JWT locally (JWKS) and passes `tenant_id` on, as described in ADR-0001.
4. Our services talk to Zitadel only over the network (OIDC, REST/gRPC API).

**License rules (the Zitadel server is AGPL-3.0):**

* Do not modify Zitadel. If we ever have to, we publish the patched fork.
* Do not import `github.com/zitadel/zitadel` in our code. Allowed: `github.com/zitadel/zitadel-go` and `github.com/zitadel/oidc` (Apache 2.0), plus `@zitadel/*` npm packages under Apache 2.0 or MIT.
* CI fails if any AGPL dependency appears in `/apps`.

## Consequences

**Good:**

* New tenant = create an Organization via API. No custom auth code.
* Standard OIDC means we can replace Zitadel later without rewriting our apps.
* One less product to build and maintain.

**Bad / risks:**

* **Critical dependency:** if Zitadel is down, nobody can log in. Already-issued tokens keep working because validation is local. It must run with at least 2 replicas and a PodDisruptionBudget, and be included in the Chaos Mesh tests.
* **AGPL questions:** some enterprise customers will ask about it during due diligence; this ADR is our answer. Hydra (Apache 2.0) was the simpler choice license-wise. We accept AGPL because of the rules above. A commercial license is available if we ever need to change Zitadel's code.
* **License can change again:** the vendor already moved from Apache 2.0 to AGPL in v3 (2025). We pin versions and re-check the license on every major upgrade.
* The team must learn Zitadel's model: Instance → Organization → Project → Application.
