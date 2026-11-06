# Feature stages

Agents write most of the code. A feature therefore ships in stages: first it is checked only by agents and automated guardrails, and it becomes `stable` after the owner has read the code. This page says what each stage promises, which guardrails it needs and how a feature moves between stages.

The term is **stage**, as in Kubernetes feature gates (`alpha`, `beta`, `stable`). The roadmap's *phases* are something else: the order in which the work is done ([Roadmap](../roadmap.md)).

Feature flags that switch features on and off by stage: [ADR-0011](../adr/0011-feature-flags.md).

## Stages

| Stage | What it promises | Checked by | Release flag | On where |
|---|---|---|---|---|
| `proto` | It compiles and does roughly the right thing. May be thrown away. | Agent: build, lint, unit tests. | Off. | Only through a local override. |
| `alpha` | Does what the acceptance criteria say. Its contracts and schema may still change. | Agent: `feature-verify` is Ready. | On for alpha tenants. | kind. |
| `beta` | Does it safely: boundaries, security, end-to-end behaviour are tested. Ready to be read. | Agent, plus every guardrail of its stage. | On. | kind and EKS staging. |
| `stable` | The owner has read it and stands behind it. | Owner: review ledger entry. | Removed. | Everywhere. |

`HAMSP-23-platform-skeleton` is merged to `master` at `beta`, before any manual review. Features below `beta` may be in `master` with their flag off.

## Two things have a stage

* **A feature**, tracked by a Linear issue. Its stage is the label `stage:proto`, `stage:alpha`, `stage:beta` or `stage:stable`, and its release flag follows it.
* **A code zone**, a set of paths in the review map (`.github/review-map.yml`). Its stage is the stage at which it was last verified, up to a commit. The review map also gives each zone a risk: `normal`, `high` or `critical`.

A feature's stage cannot be higher than the stage of the zones it changed.

## Gates per stage

**Baseline, every stage.** Every pull request passes the required CI checks: lint, formatting, type checks, secret scanning, all unit tests, the build for every architecture, the licence check and the dependency and image scanners. CI repeats the pre-commit and pre-push checks, because local hooks can be skipped. The layers are in [Testing](testing.md), section 1. The baseline does not depend on the stage, so it is not in the table.

**Gates.** ● required. A blank cell means not required at that stage.

| ID | Gate | Checked by | `proto` | `alpha` | `beta` | `stable` |
|---|---|---|---|---|---|---|
| G-01 | **Flag off is safe.** Everything passes with the release flag off, and the off path is the old behaviour or a refusal. | CI | ● | ● | ● | (no flag) |
| G-02 | **Acceptance criteria.** Every `AC-n` is traced to a test that ran and passed. | `feature-verify` | | ● | ● | ● |
| G-03 | **Contracts.** Every input that crosses a service boundary has a schema in the repo, the code passes its examples, and the owner approved the schema. Schema files have the owner as code owner, so a schema that is merged has been reviewed. | CI, code owners | | ● | ● | ● |
| G-04 | **QA checklist** ([Testing](testing.md), section 4). | `feature-verify` | | `basic` | `full` | `full` |
| G-05 | **Quality scenarios.** The touched `QS-xx` are listed with their recommended load and chaos runs; each run that happened is recorded. Informational: it never blocks a stage or a release. | `feature-verify` | | listed | listed | recorded or *not run* |
| G-06 | **Owner review.** The owner has read the code; an entry in the review ledger up to a commit. | Owner | | | | ● |
| G-07 | **Plan approved.** The owner approved the integration, contract and end-to-end rows of the plan checklist and the touched QS before implementation ([Review](review.md), R-1). | Owner | | ● | ● | ● |
| G-08 | **Structure approved.** Architecture fitness functions pass, and the owner approved the change to the architecture rules since the feature started ([Review](review.md), R-4). The code is not read at this stage. | CI, owner | | | ● | ● |

What the owner reads at each gate is in [Review](review.md).

A gate whose tooling is not built yet does not block. `feature-verify` reports it as *not checked*, and the gap is visible in the feature's QA record.

## Moving between stages

| Transition | Who | What it takes |
|---|---|---|
| none → `proto` | Agent | A Linear issue with a target stage. A release flag in the registry. |
| `proto` → `alpha` | Agent | `feature-verify` Ready for `alpha`. The decisions it depends on are Accepted. |
| `alpha` → `beta` | Agent, owner | `feature-verify` Ready for `beta`; the owner approved the structure (G-08). |
| `beta` → `stable` | Owner | A "Stabilize X" issue assigned to the owner. Review and ledger entry (G-06), release flag and the old code path removed. |
| any → lower | Automatic | See below. |

An issue is Done when the feature reaches the issue's target stage. Stabilization is always its own issue.

**Going down.** A stage is a statement about a commit, not about a feature forever:

* A change to a `stable` zone puts it back to `beta` until the owner has read the delta. The ledger shows what has changed since the last review.
* A guardrail that starts failing for a feature (a new scanner finding, a coverage drop) blocks its next promotion and is fixed before anything else in that zone.
* A zone whose `review_by` date has passed is flagged in CI. It is promoted or deleted.

**Decisions are not shipped by agents.** Issues labelled `decision` and ADRs become Accepted only through the owner. An agent may prepare the options and implement after the decision. A feature that depends on an open decision stays at `proto`.

## Critical zones

`critical` zones are where a bug leaks data between tenants or loses money: RLS and migrations of tenant tables, JWT validation and `tenant_id` propagation in the API Gateway, the `x-tenant-id` contract, UsageWriteAPI acknowledgement and deduplication, network policies.

* They get the extra checklist items marked *critical* in [Testing](testing.md) already at `alpha`, and 90% diff coverage instead of 80%.
* They are first in the queue for `stable`, bottom-up: the core before the features on top of it.
* Semgrep rules raise a change to `critical` when a dangerous construct (tenant context, raw SQL, JWT parsing, tenant headers) appears outside a marked zone.
* **A flag never switches off a security control.** RLS, token validation, the tenant header rules and network policies are not behind flags.

## Flags

[ADR-0011](../adr/0011-feature-flags.md) has the implementation. The rules that matter for stages:

* **Three kinds of flags, never mixed:**
  * **release** flags follow a feature's stage and are removed at `stable`;
  * **ops** flags are kill switches and stay; their code is reviewed like any `stable` code;
  * **entitlements** (what a tenant's plan includes) are not flags; they are Tenant Management data (v2, with billing).
* **Off is safe.** The off state is the old behaviour or a refusal, never a weaker check.
* **Both states are tested** until the flag is removed: off from `proto`, on from `alpha`, rollback from `beta` ([Testing](testing.md), section 3). Otherwise the old path rots while the flag is on.

## Code structure

* **`stable` code does not import code of a lower stage** (depguard, per the review map).
* **Branch by abstraction.** The `stable` package declares an interface. The new implementation lives in its own package. `cmd/` builds a switch that holds both implementations and picks one per request from the flag, because a flag can differ per tenant. Only `cmd/` and the flags package may see both.
* At `stable` the old implementation, the switch and the flag are deleted in the same change.

## Database migrations

The schema is shared by every tenant, so a migration cannot hide behind a flag.

* `proto` and `alpha`: only additive migrations (new tables, nullable columns, new indexes created concurrently).
* Removing or retyping a column or table (the contract step) happens only after the feature that stops using it is `stable`.
* Tenant tables follow ADR-0002 at every stage: RLS forced, checked in CI.

## Contracts

`docs/contracts/` holds the contracts between services ([ADR-0010](../adr/0010-usage-event-contracts.md) for usage events).

* A contract introduced by an `alpha` or `beta` feature may change without a new version, as long as every participant changes in the same pull request.
* From `stable` on, a contract changes only through the versioning rules of ADR-0010.

## Environments

| Environment | Stages on |
|---|---|
| Developer machine | All, `proto` through a local override. |
| kind (prototype, CI) | `alpha` for alpha tenants, `beta` and `stable` for everyone. |
| EKS staging | `beta` and `stable`. |
| Production | Not defined yet; `stable` only. |

## Linear and the feature workflow

* Every feature issue has one `stage:*` label: its target stage.
* `feature-clarify` asks for the target stage when it is missing.
* `feature-verify` checks the gates of the target stage, reports Ready or Not ready for it and writes the feature's QA record (`qa/features/HAMSP-N.yaml`, [Testing](testing.md), section 8).
* Stabilization is a "Stabilize X" issue assigned to the owner.
