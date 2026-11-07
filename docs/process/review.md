# Review

What the owner reads, when, and what automated checks cover instead. The owner's time is the scarce resource: agents and CI check everything that can be stated as a rule, and the owner reads what cannot.

Gates are defined in [Feature stages](feature-stages.md); the QA checklist is in [Testing](testing.md).

## What the owner reviews

| ID | Review | When | What the owner reads | Gate |
|---|---|---|---|---|
| R-1 | **Plan checklist** | Before implementation, for `alpha` and higher. | The integration, contract and end-to-end rows of the plan checklist and the touched QS ([Testing](testing.md), section 5). Other details of the plan only when something is unclear. | G-07 |
| R-2 | **Contracts** | Every pull request that changes a schema in `docs/contracts/`. | The schema diff, before merging. The owner merges every pull request, so a merged schema has been read. | G-03 |
| R-3 | **Verify deviations** | After each `feature-verify` run. | Only the deviations from the approved plan and the items `feature-verify` added. | G-04 |
| R-4 | **Structure** | Before `beta`. | The generated component views of the touched services and the c4sync structural diff since the feature started, including the change to the architecture rules (sections below). Not the code. | G-08 |
| R-5 | **Code** | Before `stable`, and for a later change to the code of a reviewed feature when the owner decides it needs one. | The feature's code, or the delta since the commit in gate G-06, listed in the pull request comment. | G-06 |

The owner does not read: unit tests, implementation below `stable`, formatting, coverage numbers, scanner output that passed. CI and `feature-verify` check those.

## Architecture fitness functions

An architecture fitness function is an automated check that the code keeps an architectural property (Ford, Parsons and Kua, *Building Evolutionary Architectures*). With them the owner reviews a rule once instead of reading every change that could break it: after that, CI checks each pull request against the rule.

| Property | Check | Runs in |
|---|---|---|
| **Allowed dependencies.** Each component (a Go package or a group of them) may depend only on the components the rules allow. | `.go-arch-lint.yml` in each Go module: the component map and the allowed edges, checked against the imports by `make c4-check` (go-arch-lint through c4sync). Unmapped packages fail. | CI |
| **Diagrams match the code.** The generated component views in `docs/architecture/c4/generated/` and the Mermaid in the C4 README are up to date. | `make c4-check`, `render.sh --check` (workflow `c4`). | CI |
| **Stages.** `stable` code does not import code of a lower stage. | depguard rules generated from the `code` globs and stages of the feature files. | CI |
| **No runtime leak into lower stages.** | The `stable` tests with flags off cover no line below `stable` ([Testing](testing.md), section 3). | CI |
| **Tenant isolation in the database.** Every multi-tenant table has RLS enabled and forced. | The existing RLS check (QS-01). | CI |
| **Tenant header.** Every service that trusts `x-tenant-id` has a network policy that admits only the API Gateway. | A check over the manifests in `prototype/k8s/apps` against [gateway-headers](../contracts/gateway-headers.md). | CI |
| **Dangerous constructs stay in critical features.** Tenant context, raw SQL, JWT parsing, tenant headers. | Semgrep rules; a match outside the code of a critical feature makes the change critical. | CI |
| **Contracts stay compatible.** | The contract checks of [ADR-0010](../adr/0010-usage-event-contracts.md). | CI |
| **Constraints.** No AGPL under `/apps` (C-03), every image builds for `arm64` (C-06). | The licence check, the multi-arch build. | CI |
| **Flags.** Every release flag has an issue, a stage and an expiry ([ADR-0011](../adr/0011-feature-flags.md)). | The flag registry checks. | CI |

Rules for the rules:

* **Rules are files in the repo.** A property that matters and can be checked becomes a rule; a review comment that repeats twice is a candidate.
* **Edges into critical features are read at every stage.** A rule change that allows a new dependency on the code of a critical feature is listed in the `feature-verify` report at every stage and waits for the owner.
* **Other rule changes are reviewed at `beta`** (R-4). Below `beta` an agent adds the rules for the components it creates, and CI enforces whatever rules exist. Before `beta` the owner reads the rule diff since the feature started: new components, new edges, new external calls.
* **A failing fitness function is fixed in the code, not in the rule.** Loosening a rule is a rule change and goes through the same review.

## Component view

The component views (C4 level 3) are generated from the Go code by [c4sync](../../tools/c4sync/README.md), so for R-4 the owner looks at a picture of the components and edges instead of reading a rule diff.

* **What is drawn.** go-arch-lint's component map (`.go-arch-lint.yml`) says which packages form a component. c4sync adds the imports between components, the libraries mapped to workspace elements, and the HTTP routes, Kafka topics and SQL tables it finds in the code. A module with several binaries is drawn as one container per binary.
* **Where it lives.** The generated DSL is committed in `docs/architecture/c4/generated/`; `workspace.dsl` includes it in the UsageWriteAPI, Usage Writer and ExampleApp containers. The gateway and the admin app have code workspaces only, until ADR-0004 and ADR-0009 are accepted. `make c4` regenerates everything.
* **What R-4 reads.** On every pull request the `c4` workflow posts the structural diff against the base branch as one comment: new components, edges, external calls, routes, topics and tables, each with `file:line`, components marked `important` first. The views of the changed components are in the run's `c4-diff` artifact. Before `beta` the owner reads the diff since the feature started (`make c4-diff BASE=<checkout of the commit the feature started from>`) and the component views of the touched services.

A feature file can name its elements of the C4 model (`c4:`), so the map links a feature to its place in the diagrams.

## Where reviews are recorded

In the feature file ([Features](../features/README.md)), as gates marked `by: owner`: G-07 (R-1), G-08 (R-4) and G-06 (R-5), which also holds the commit the code was read up to. Every pull request gets a comment listing the features whose code it changes, with their stage and the files changed since that commit, so the owner reads only the delta and decides: move G-06 to the new commit, or lower the `stage`. Nothing is lowered automatically.
