# ADR-0011: Feature flags are OpenFeature with flagd evaluated in-process from a GitOps file

Date: 2026-11-06

## Status

Proposed

Raised with the feature stages ([Feature stages](../process/feature-stages.md)); implemented in HAMSP-63. Keeps C-04 (no manual changes in the cluster) and C-03 (no AGPL code under `/apps`).

## Context

Features ship in stages: `proto`, `alpha`, `beta`, `stable`. A feature below `stable` must be switchable at runtime, and per tenant: an `alpha` feature is on only for alpha tenants. The flag must be read on the request path of the API Gateway, which every API call passes through and which must keep serving when its dependencies fail.

What we need:

* per-tenant targeting by `tenant_id`;
* no extra network call and no new failure point on the request path;
* the flag state in Git and deployed by ArgoCD (C-04), so every change is a reviewed pull request;
* the same answer in every service for the same tenant;
* a check that ties each flag to a Linear issue and a stage, and that removes it at `stable`;
* a way to swap the backend later without touching call sites.

Services are separate Go modules (`apps/gateway`, `apps/usage`, `apps/exampleapp`, `apps/admin`); there is no shared Go module yet.

## Decision

### 1. OpenFeature API, flagd in-process provider, flags from a file

Services call the [OpenFeature](https://openfeature.dev) Go SDK. The provider is flagd's Go provider in in-process mode with an offline file (`flagd.WithInProcessResolver()`, `flagd.WithOfflineFilePath(...)`): flags are evaluated in the service's memory from a JSON file in flagd's format. No flagd service runs.

The file is a ConfigMap mounted as a volume. The provider watches it, so a change reaches the pods without a restart, usually within about a minute (the kubelet's ConfigMap sync).

### 2. One registry, generated files

`flags/registry.yaml` is the source of truth:

```yaml
tenant-suspend:
  kind: release          # release | ops
  stage: alpha           # proto | alpha | beta
  issue: HAMSP-52
  review_by: 2027-01-15
  description: API Gateway rejects requests of a suspended tenant.
```

A small generator (`libs/flags/cmd/flagsgen`) writes from it:

* `flags.json` per environment, from the stage:

  | Stage | kind | EKS staging |
  |---|---|---|
  | `proto` | off | absent (off) |
  | `alpha` | on for tenants in the shared evaluator `alpha-tenants` | off |
  | `beta` | on | on |

  `alpha-tenants` is a list of `tenant_id`s in the registry, per environment.
* Go constants for the flag keys, so call sites cannot misspell a key.

`ops` flags have no stage: they are on unless the registry says otherwise. A local override file, not committed, switches `proto` flags on a developer's machine.

### 3. Evaluation context

* `targetingKey` is the `tenant_id`: the API Gateway takes it from the validated JWT, the services from `x-tenant-id` (`docs/contracts/gateway-headers.md`).
* Attributes: `env` (`kind`, `staging`) and `service`.
* Every service evaluates its own flags. The file is the same everywhere, so the answer is the same; no header carries flag values between services.
* The admin app's BFF evaluates the flags for the signed-in user and serves them to the SPA at `/api/flags`. The SPA never reads the file.

### 4. Code: a thin shared module and branch by abstraction

`libs/flags` is a Go module that each service pulls in with a `replace` directive. It holds:

* setup of the OpenFeature provider from the mounted file, with a defaults-only fallback;
* the generated key constants;
* `Switch[T]`: holds an old and a new implementation of an interface and returns one per request from a boolean flag and the request's tenant;
* an in-memory provider for tests.

A `stable` package declares the interface; the new implementation lives in its own package; `cmd/` wires them through `Switch`. `stable` code never imports the new package (depguard), and only `cmd/` and `libs/flags` see both. Flags are not read deep inside business logic.

### 5. Failure: off is safe

* Every flag has a default in code: `false` for `release` flags.
* A missing or broken file means every flag takes its default. The error goes to the log and, once HAMSP-61 is done, to a metric.
* The off state is the old behaviour or a refusal. A flag never switches off a security control: RLS, token validation, the tenant header rules and network policies are not behind flags.

### 6. CI

| Rule | Fails when |
|---|---|
| Generated files are current | `flags.json` or the Go constants differ from what `flagsgen` produces |
| Valid flagd file | a `flags.json` does not pass flagd's JSON schema |
| Every flag has an issue | `issue` is empty or not a `HAMSP-N` |
| Stage matches Linear | the registry stage differs from the issue's `stage:*` label |
| No `stable` flags | a `release` flag is in the registry with stage `stable`, or its issue is `stage:stable` |
| Not expired | `review_by` has passed |
| Used and declared | a registry flag is not referenced in code, or code references a key not in the registry |

The Linear check needs an API token in CI; until it exists, that rule runs in `feature-verify` instead.

### 7. Tests

* Unit tests use the in-memory provider and run each flagged path with the flag on and off (table tests).
* The kind e2e suite runs with one alpha tenant and one normal tenant.

## Considered options

| Option | Verdict |
|---|---|
| flagd as a service (sidecar or central) | Rejected for now. A network call or one more process on the request path, and a new failure point. The in-process mode reads the same file format, so switching later changes only provider setup. |
| Unleash, GrowthBook, Flagsmith | Rejected. A stateful service with its own database and UI; flag changes leave Git, which goes against C-04. |
| Environment variables | Rejected. No per-tenant targeting; every change restarts pods. |
| Our own flags table in PostgreSQL | Rejected. Rebuilds targeting, caching and a UI; flag changes leave Git. |
| Hand-written config struct per service, no OpenFeature | Rejected. Each service would reimplement targeting; no common API to move to a backend later. |
| A Kubernetes operator for flags (flagd's `FeatureFlag` CRD) | Not now. Needs the OpenFeature operator in the cluster; a plain ConfigMap does the job while flags are few. |

**Revisit when** someone other than the owner needs to change flags without a pull request, flags need percentage rollouts or experiments with metrics, or production needs a change to apply in seconds rather than about a minute. A flagd service or a managed backend then replaces the file provider behind the same OpenFeature calls.

## Consequences

* Every release flag costs a registry entry, a Linear issue and tests in both states, and it must be removed at `stable`. Flags are cheap to add and visible until they are gone.
* Changing a flag is a pull request and an ArgoCD sync, about a minute end to end. Good enough for the prototype and staging; not an incident kill switch measured in seconds.
* `libs/flags` is the first shared Go module. Its changes affect every service, so it is a `high` risk zone in the review map.
* Entitlements (what a tenant's plan includes) are not flags. They belong to Tenant Management, with billing in v2.
