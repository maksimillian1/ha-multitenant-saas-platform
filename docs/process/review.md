# Review

What the owner reads, when, and what automated checks cover instead. The owner's time is the scarce resource: agents and CI check everything that can be stated as a rule, and the owner reads what cannot.

Gates are defined in [Feature stages](feature-stages.md); the QA checklist is in [Testing](testing.md).

## What the owner reviews

| ID | Review | When | What the owner reads | Gate |
|---|---|---|---|---|
| R-1 | **Plan checklist** | Before implementation, when the target stage is `alpha` or higher. | The integration, contract and end-to-end rows of the plan checklist and the touched QS ([Testing](testing.md), section 5). Other details of the plan only when something is unclear. | G-07 |
| R-2 | **Contracts** | Every pull request that changes a schema in `docs/contracts/`. | The schema diff, before merging. The owner merges every pull request, so a merged schema has been read. | G-03 |
| R-3 | **Verify deviations** | After each `feature-verify` run. | Only the deviations from the approved plan and the items `feature-verify` added. | G-04 |
| R-4 | **Structure** | Before `beta`. | The change to the architecture rules since the feature started (section below), and the component view once it is generated. Not the code. | G-08 |
| R-5 | **Code** | Before `stable`, and again for every later change to the code of a `stable` feature. | The feature's code, or the delta since its last `reviews` entry. | G-06 |

The owner does not read: unit tests, implementation below `stable`, formatting, coverage numbers, scanner output that passed. CI and `feature-verify` check those.

## Architecture fitness functions

An architecture fitness function is an automated check that the code keeps an architectural property (Ford, Parsons and Kua, *Building Evolutionary Architectures*). With them the owner reviews a rule once instead of reading every change that could break it: after that, CI checks each pull request against the rule.

| Property | Check | Runs in |
|---|---|---|
| **Allowed dependencies.** Each component (a Go package or a group of them) may depend only on the components the rules allow. | A declared component map with allowed edges, checked against the imports (for example go-arch-lint or depguard). Unmapped packages fail. | CI |
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
* **Edges into critical features are read at every stage.** A rule change that allows a new dependency on the code of a critical feature is listed in the `feature-verify` report, whatever the target stage, and waits for the owner.
* **Other rule changes are reviewed at `beta`** (R-4). Below `beta` an agent adds the rules for the components it creates, and CI enforces whatever rules exist. Before `beta` the owner reads the rule diff since the feature started: new components, new edges, new external calls.
* **A failing fitness function is fixed in the code, not in the rule.** Loosening a rule is a rule change and goes through the same review.

## Component view

A C4 component view (level 3) generated from the code makes R-4 faster: the owner looks at a picture of the feature's components and edges instead of reading a rule diff.

Structurizr can extract components from Java code only. For Go it needs a small generator: packages and imports from `go list -json`, HTTP routes, Kafka topics and tables from the source, written as a DSL fragment that `docs/architecture/c4/workspace.dsl` includes. The generated fragment is committed, so a pull request shows the structural change as a diff.

Until the generator exists, R-4 reads the rule diff.

A feature file can name its elements of the C4 model (`c4:`), so the map links a feature to its place in the diagrams.

## Where reviews are recorded

In the feature file ([Features](../features/README.md)): `plan_approved` (R-1), `structure_approved` (R-4) and `reviews` (R-5). Each `reviews` entry has the date, the commit reviewed up to and its scope: `full`, or `delta` since the previous entry. The generated map shows a `stable` feature whose code changed after its last entry as `beta`, with that commit, so the owner reads only the delta.
