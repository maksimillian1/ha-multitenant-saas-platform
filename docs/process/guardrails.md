# Guardrails

Every guardrail of the project, where its rule is written and what checks it today. [Testing](testing.md), [Review](review.md), [Feature stages](feature-stages.md) and `AGENTS.md` describe the rules in the present tense; this page says which of them actually run.

* **Status** is `runs`, with the workflow, test or tool that runs it, or `missing`, with the issue that adds it.
* A row links to the rule and does not repeat it.
* A new guardrail, or a missing one that starts running, changes its row in the same pull request.

**Layers:** *agent* (`AGENTS.md`, the skills in `.agents/skills/`), *L1* (pre-commit), *L2* (pre-push and the agent hook), *CI* (every pull request), *gate* (`feature-verify`, per feature and stage), *owner* (the owner's reviews). L1 to CI are defined in [Testing](testing.md), section 1.

Runtime controls are not rows here: RLS policies, token validation, the tenant header rules, network policies and the functions of [ADR-0008](../adr/0008-usage-store-on-timescaledb.md) §4 are code of critical features ([Feature stages](feature-stages.md), "Critical features"). Their tests are the QA checklist, with control canaries (T-12) for the controls themselves.

## Agent

| Guardrail | Layer | Rule in | Checked by | Status |
|---|---|---|---|---|
| Linear workflow: issue ID in branch, commits and PR; closing comment; new problems go to Triage | agent | `AGENTS.md` | the owner, at merge | runs |
| Feature workflow: clarify, plan, verify | agent | `AGENTS.md`, `.agents/skills/` | the skills; `featuremap -check` checks what they write (`features.yml`) | runs |
| Development rules: do not weaken a check, do not change a test to make it pass, read the feature file first, never skip the hooks | agent | `AGENTS.md`, "Guardrails" | the owner, at merge; the CI checks below | runs |

## Local hooks

| Guardrail | Layer | Rule in | Checked by | Status |
|---|---|---|---|---|
| gofmt, goimports and golangci-lint on changed files | L1 | [Testing](testing.md) §1 | `lefthook.yml`: `go-fmt`, `go-lint-*` | runs |
| oxlint and `tsc` on changed files | L1 | [Testing](testing.md) §1 | `lefthook.yml`: `spa-oxlint`, `spa-tsc` | runs |
| gitleaks on staged changes | L1 | [Testing](testing.md) §1 | `lefthook.yml`: `gitleaks` | runs |
| Claude Code runs the pre-commit checks before every `git commit` | L1 | `AGENTS.md`, "Guardrails" | `.claude/hooks/pre-commit.sh` (`.claude/settings.json`) | runs |
| semgrep on changed files | L1 | [Testing](testing.md) §1 | lefthook | missing, HAMSP-46 |
| Unit and contract tests of the touched packages before push | L2 | [Testing](testing.md) §1 | lefthook `pre-push` | missing, HAMSP-87 |
| The agent runs lint and the touched tests before it reports done | L2 | `AGENTS.md`, "Guardrails" 5; [Testing](testing.md) §1 | the agent itself; nothing enforces it | runs |

## CI baseline

Every pull request, every stage ([Feature stages](feature-stages.md), "Gates per stage").

| Guardrail | Layer | Rule in | Checked by | Status |
|---|---|---|---|---|
| `go vet` and all Go tests | CI | [Testing](testing.md) §1 | `admin.yml`, `gateway.yml`, `exampleapp.yml`, `usage.yml` | runs |
| Database tests fail the job when skipped | CI | `exampleapp.yml`, `usage.yml` | the `Fail if database tests were skipped` step | runs |
| Go tests with `-race` | CI | [Testing](testing.md) §1 | none | missing, HAMSP-85 |
| gofmt, goimports and golangci-lint on every Go module | CI | [Testing](testing.md) §1 | `lint.yml`, job `go` (shared `.golangci.yml`); `usage.yml` adds its depguard rules | runs |
| gitleaks on the PR's commits | CI | [Testing](testing.md) §1 | `lint.yml`, job `gitleaks` | runs |
| The hooks refuse the bad changes they are for | CI | HAMSP-40 | `lint.yml`, job `hooks` (`tools/hooktest/test.sh`) | runs |
| Admin SPA: oxlint, `tsc`, unit tests, build | CI | [Testing](testing.md) §1 | `admin.yml`, job `spa` | runs |
| Strict TypeScript, Zod checks of BFF responses | CI | HAMSP-41 | none | missing, HAMSP-41 |
| Strict input decoding in Go services | CI | HAMSP-42 | none | missing, HAMSP-42 |
| Go builds for `arm64` (C-06) | CI | [requirements](../architecture/requirements.md) C-06 | `Build for arm64` step in the four Go workflows | runs |
| Every image we build is multi-arch (C-06) | CI | [requirements](../architecture/requirements.md) C-06 | `timescaledb.yml` only; the app images build for the runner's architecture | missing, HAMSP-27 |
| No AGPL dependency under `/apps` (C-03) | CI | [requirements](../architecture/requirements.md) C-03 | `licenses.yml`: Go modules with a negative fixture, and npm | runs |
| govulncheck, trivy, npm audit | CI | [Testing](testing.md) §1 | none | missing, HAMSP-46 |
| Generated Go and TS types match `openapi.yaml` | CI | [ADR-0009](../adr/0009-admin-app-go-bff-react-spa.md) | `admin.yml` | runs |
| Usage binaries link only their own packages | CI | `apps/usage/README.md` | `usage.yml`: depguard, `scripts/check-linked-deps.sh`, the `images` job | runs |
| TimescaleDB image works and updates from the previous release | CI | [ADR-0008](../adr/0008-usage-store-on-timescaledb.md) | `timescaledb.yml`, `smoke-test.sh` | runs |
| Diff coverage: 80% of changed lines, 90% in critical code | CI | [Testing](testing.md) §6 | none | missing, HAMSP-43 |
| Package coverage does not drop (ratchet) | CI | [Testing](testing.md) §6 | none | missing, HAMSP-43 |
| Feature files are valid, the stage is backed by its gates, the map is current | CI | [Feature stages](feature-stages.md), [Features](../features/README.md) | `features.yml`: `featuremap -check` | runs |
| Every verified test exists and has a `Verifies:` comment | CI | [Testing](testing.md) §8 | `features.yml`: `featuremap -check` | runs |
| PR comment with the features whose code changed | CI | [Review](review.md), "Where reviews are recorded" | `features.yml`, job `touched` | runs |

## Architecture fitness functions

From [Review](review.md), "Architecture fitness functions".

| Guardrail | Layer | Rule in | Checked by | Status |
|---|---|---|---|---|
| Allowed dependencies between components | CI | [Review](review.md) | `.go-arch-lint.yml` in each Go module, `make c4-check` in `c4.yml` | runs |
| Diagrams match the code | CI | [Review](review.md) | `c4.yml`: `make c4-check`, `render.sh --check` | runs |
| Structural diff of the PR for the owner | CI | [Review](review.md), "Component view" | `c4.yml`, job `diff` | runs |
| `stable` code does not import code of a lower stage | CI | [Review](review.md), [Feature stages](feature-stages.md) | none | missing, HAMSP-86 |
| No runtime leak into lower stages | CI | [Testing](testing.md) §3 | none | missing, HAMSP-86 |
| RLS enabled and forced on every multi-tenant table (QS-01) | CI | [ADR-0002](../adr/0002-data-isolation-strategy.md), [Review](review.md) | `TestMigrationsLeaveNoUnprotectedTables` and the isolation tests in `apps/exampleapp/store` and `apps/usage/internal/store` (`exampleapp.yml`, `usage.yml`) | runs |
| Services that trust `x-tenant-id` admit only the API Gateway | CI | [gateway-headers](../contracts/gateway-headers.md), [Review](review.md) | none | missing, HAMSP-70 |
| Dangerous constructs outside critical code make the change critical | CI | [Review](review.md), [Feature stages](feature-stages.md) | semgrep rules | missing, HAMSP-46 |
| Contracts stay compatible | CI | [ADR-0010](../adr/0010-usage-event-contracts.md) | `contracts.yml`: JSON Schema examples, `oasdiff` against the base branch | runs |

## Feature gates

From [Feature stages](feature-stages.md), "Gates per stage". `feature-verify` reports a gate whose tooling does not exist as *not checked*.

| Guardrail | Layer | Rule in | Checked by | Status |
|---|---|---|---|---|
| G-01 Flag off is safe | gate | [Feature stages](feature-stages.md) | `feature-verify`: `n/a` for a feature without a release flag | runs |
| G-02 Acceptance criteria | gate | [Feature stages](feature-stages.md) | `feature-verify` | runs |
| G-03 Contracts | gate | [Feature stages](feature-stages.md) | `contracts.yml`, `feature-verify`, the owner (R-2) | runs |
| G-04 QA checklist | gate | [Testing](testing.md) §4 | `feature-verify`; the items below that need tooling | runs |
| G-05 Quality scenarios, listed or recorded | gate | [Testing](testing.md) §7 | `feature-verify`, `docs/features/qs-runs.md` | runs |
| G-06 Owner review | owner | [Feature stages](feature-stages.md) | the owner; `featuremap -check` rejects a `stable` stage without it | runs |
| G-07 Plan approved | owner | [Feature stages](feature-stages.md) | the owner; `featuremap -check` | runs |
| G-08 Structure approved | owner | [Feature stages](feature-stages.md) | the owner, `c4.yml`; the fitness functions above, some missing | runs |

QA checklist items ([Testing](testing.md) §4). `feature-verify` checks each one; these need tooling of their own:

| Guardrail | Layer | Rule in | Checked by | Status |
|---|---|---|---|---|
| T-01–T-03, T-05–T-08 | gate | [Testing](testing.md) §4 | ordinary tests, `feature-verify` | runs |
| T-04 Authorization matrix | gate | [Testing](testing.md) §4 | generated role × tenant × endpoint tests | missing, HAMSP-47 |
| T-09 End-to-end on kind | CI | [Testing](testing.md) §4 | `prototype/e2e` in CI | missing, HAMSP-49 |
| T-09 Playwright for the admin app | CI | [Testing](testing.md) §4 | Playwright against BFF and Zitadel | missing, HAMSP-48 |
| T-10 Diff coverage | CI | [Testing](testing.md) §6 | see CI baseline | missing, HAMSP-43 |
| T-11 Fuzzing | gate | [Testing](testing.md) §4 | Go fuzz targets | missing, HAMSP-47 |
| T-12 Control canaries | gate | [Testing](testing.md) §4 | a test run with one control disabled | missing, HAMSP-50 |

Mutation testing and load and chaos runs are optional ([Testing](testing.md) §4 and §7) and block nothing, so they have no row. Neither do release flags: they are optional until a feature needs one ([Feature stages](feature-stages.md), "Flags"), and their checks come with HAMSP-63.

## Owner reviews

From [Review](review.md), "What the owner reviews".

| Guardrail | Layer | Rule in | Checked by | Status |
|---|---|---|---|---|
| R-1 Plan checklist | owner | [Review](review.md) | the owner, recorded as G-07 | runs |
| R-2 Contracts: schema diff before merge | owner | [Review](review.md) | the owner, who merges every PR | runs |
| R-3 Verify deviations | owner | [Review](review.md) | the owner, from the `feature-verify` report | runs |
| R-4 Structure | owner | [Review](review.md) | the owner, from the `c4.yml` diff; recorded as G-08 | runs |
| R-5 Code | owner | [Review](review.md) | the owner, from the `touched` comment; recorded as G-06 | runs |
