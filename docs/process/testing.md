# Testing

How features are tested and where the results are kept. [Feature stages](feature-stages.md) says which stage needs which gate; this page is the detail behind gates G-01, G-04, G-05 and G-07 there. What the owner reviews is in [Review](review.md).

Three rules hold everywhere:

* **A test that did not run is not evidence.** Neither is reading the code.
* **Every check is traced.** A test says what it verifies (an `AC-n`, a checklist item `T-xx`, a `QS-xx`), and the result of each run is kept in the repo.
* **Not applicable is written down.** A checklist item that does not apply gets `n/a` and a reason; it is never skipped silently.

## 1. Where checks run

| Layer | Time | What runs | Can be skipped? |
|---|---|---|---|
| **L1: pre-commit** | seconds | gofmt, golangci-lint and oxlint on changed files, `tsc`, gitleaks, semgrep on changed files | Yes, with `--no-verify`. |
| **L2: pre-push and the agent hook** | about a minute | unit tests of the touched packages, contract tests of the touched contracts | Yes. |
| **L3: CI, every pull request** | no limit | L1 and L2 again on the whole change; all unit tests with `-race`; build for `amd64` and `arm64`; licence check; govulncheck, trivy, npm audit; the flag-off suite (section 3); diff coverage (section 6) | No. Required checks. |

CI repeats L1 and L2 because local hooks can be skipped. CI is the only layer that enforces anything; L1 and L2 exist to fail early and cheaply. Everything in L3 is the *baseline* of [Feature stages](feature-stages.md): it applies to every change at every stage.

Heavier tests (end-to-end on kind, fuzzing, control canaries, load, chaos) are not part of every pull request. They run when the checklist asks for them (section 4), through `feature-verify`.

## 2. Kinds of tests

| Kind | What it is here |
|---|---|
| Unit | One package, no network, no database. |
| Integration | Real PostgreSQL (with RLS), Kafka or Zitadel in containers; one service. |
| Contract | The code against the examples of a schema in `docs/contracts/` ([ADR-0010](../adr/0010-usage-event-contracts.md)). |
| End-to-end | The kind cluster, through the API Gateway with real tokens; Playwright for the admin app. |
| Fuzz | Go fuzz targets for decoders and parsers of untrusted input. |
| Control canary | One critical control is removed on purpose; its test must fail. Shows that the test can see the breach (T-12). |
| Mutation | The code is changed automatically, many times; a test must fail each time. Optional, for pure Go logic (section 4). |
| Load | k6 against kind or EKS. |
| Chaos | Chaos Mesh kills pods, nodes or dependencies during a run. |

## 3. Feature flags: both states are tested

Every release flag ([ADR-0011](../adr/0011-feature-flags.md)) needs tests in both states for as long as it exists.

* **Off** (gate G-01, from `proto`). The application works with the flag off, and the off path is the old behaviour or a refusal. CI runs the whole test suite with every release flag at its default, which is off. Code below `alpha` is merged with its flag off, so for everyone else this is the state that matters.
* **On** (from `alpha`). The acceptance criteria are tested with the flag on. The kind end-to-end suite runs with one alpha tenant, which has every `alpha` flag on, and one normal tenant.
* **Rollback** (from `beta`, for a feature that writes data). Flag on, the feature writes data, flag off: the old path still works for that tenant and does not fail on the data the feature left behind. A flag that cannot be switched off safely is not a flag.
* **No leak into lower stages** (from `beta`). The tests of `stable` packages, run with every flag off, cover no line of a package below `stable` (`go test -coverpkg`). That shows no `stable` path reaches code of a lower stage at runtime; depguard shows the same for imports.

Unit tests set flags with the in-memory provider from `libs/flags`, as a table over both values.

## 4. QA checklist

Gate G-04. `basic` is required at `alpha`, `full` at `beta` and `stable`. Items marked *critical* are required at `basic` already when the change touches a `critical` zone of the review map. **Kind** decides what the owner reviews in the plan (section 5): integration, contract and end-to-end rows.

| ID | Check | Kind | Applies when | basic | full |
|---|---|---|---|---|---|
| T-01 | **Tenant boundaries.** Missing, foreign or forged `tenant_id`; tenant A reads, changes or deletes tenant B's record and gets nothing (404 or an empty result). Two tenants, RLS on. | integration | Always: every feature is multi-tenant. | ● | ● |
| T-02 | **Contracts.** The code passes the examples of every schema it produces or consumes; a broken body gets 400, not 500. | contract | The feature has an input or output across a service boundary. | ● | ● |
| T-03 | **Input boundaries.** Empty, 0, max, max+1, wrong types, malformed body, body over the size limit. | unit | The feature takes input. | | ● |
| T-04 | **Authorization.** Every new endpoint or action, for every role, in its own tenant and in another tenant. | integration | The feature adds an endpoint or an action. | *critical* | ● |
| T-05 | **Retries and idempotency.** The same request or event twice gives one effect. | integration | The feature writes data or sends events. | | ● |
| T-06 | **Dependency failure.** Timeout, refused connection or error from each dependency: a clear error to the caller, no hang, no partial write. | integration | The feature calls a database, Kafka, Zitadel or another service. | | ● |
| T-07 | **Flag rollback.** Section 3. | integration | The feature writes data. | | ● |
| T-08 | **Migrations.** Applied to a database with data; additive only below `stable`; RLS forced on new tenant tables. | integration | The feature has a migration. | ● | ● |
| T-09 | **End-to-end.** The main flow on kind, through the API Gateway with real tokens; Playwright for admin app pages. | end-to-end | The feature is reachable from outside. | | ● |
| T-10 | **Diff coverage.** At least 80% of changed lines, 90% in `critical` zones (section 6). | metric | Always. | | ● |
| T-11 | **Fuzzing.** A fuzz target for each new decoder or parser of untrusted input, run for a fixed time. | fuzz | The feature parses untrusted input. | *critical* | ● |
| T-12 | **Control canary.** Each `critical` control the feature adds or changes has a test that is shown to fail when the control is removed: the cross-tenant test fails with the RLS policy dropped, the token test fails with signature checking off, the network test fails without the policy. | integration | The change adds or changes a `critical` control. | *critical* | ● |

The acceptance criteria themselves are gate G-02, not part of this list.

**Why a control canary and not general mutation testing.** Mutation testing changes code on purpose and expects a test to fail; it shows tests that run code without checking it. Run over a whole package it needs one test run per mutant, which only fast unit tests can afford, and Go mutators do not touch where most of our controls live: RLS policies in SQL, network policies in YAML, Kafka settings. The control canary is the same idea, applied once, by hand, where it matters: one deliberate break per critical control, at the integration level. General mutation testing stays optional, for pure Go logic in `critical` zones such as the deduplication key or claim parsing.

## 5. Checklist lifecycle

One checklist, two passes, with the same `T-xx` IDs.

1. **Plan pass (`feature-plan`).** For each item: *applies*, *does not apply* (one line why) or *unsure*. For items of kind integration, contract and end-to-end that apply, one line on what is tested: the scenario and the components involved. No test names and no unit-level detail. The touched QS are listed; load and chaos runs are not chosen yet.
2. **Owner review of the plan** (gate G-07). Required when the target stage is `alpha` or higher. The owner reviews the integration, contract and end-to-end rows and the touched QS, nothing else. Approval is recorded in the issue before implementation starts.
3. **Verify pass (`feature-verify`).** Adds the detail from the actual change: test names, cases, results. It may add items or cases the diff shows, and it proposes load and chaos runs (section 7), which never block.
4. **Ratchet.** An item the owner approved as *applies* cannot become `n/a` or be dropped by `feature-verify`. It is reported as a deviation from the plan and needs the owner's approval.
5. **Gaps are not fixed during verification.** A missing test makes the verdict Not ready; writing it is a separate step.
6. **Plan against result.** The QA record keeps both passes per item (`planned`, `verified`). The verify report lists the deviations first. After verification the owner reads only those.

## 6. Diff coverage

Diff coverage counts only the lines the change adds or modifies: of those lines, how many ran during the tests. Package coverage is a poor signal for new code: old untested code pulls the number down, and fifty untested new lines in a large package barely move it. Diff coverage looks only at what the change is responsible for.

It shows that new code runs, not that the tests check it; the control canary (T-12) covers that for critical controls. Package coverage is still reported and may not drop (a ratchet), but it gates nothing on its own.

## 7. Quality scenarios

`feature-plan` lists the quality scenarios a feature touches. A QS check is one of two things:

* **A functional test** that is cheap: an integration or kind test at low load, such as QS-01 (cross-tenant read) or QS-06 (the same event three times). These are ordinary tests: they are in the test map and count like any other.
* **A load or chaos run**: k6, Chaos Mesh, EKS. These are expensive, and `feature-verify` proposes them from the actual change. They are **recommended, never required**: they block no stage and no release, including production. When one runs, its result is recorded; when it does not, the record says *not run*.

Which runs are recommended for a feature, by what it changes:

| The feature changes | Recommended runs |
|---|---|
| The request path: API Gateway, a service behind it | QS-03 (noisy tenant), QS-10 (rolling deploy) |
| Rate limits or quotas | QS-03, QS-15, QS-16 |
| The usage pipeline: UsageWriteAPI, Kafka, Usage Writer | QS-07, QS-08, QS-09, QS-17 |
| The main database: schema, queries, CNPG settings | QS-04 (failover), QS-12 (long query), QS-13 (restore) |
| Authentication or Zitadel | QS-11 |
| Tenant lifecycle | QS-18 |
| Network policies or tenant namespaces | QS-02 |
| Node pools, Karpenter, Spot | QS-05, QS-14 |

The QS status in [requirements.md](../architecture/requirements.md) is the latest recorded run.

## 8. Where results are kept

In the repo, next to the code, not in a separate test management tool such as TestRail. A change to the code and to its test record is reviewed in the same pull request, and the history is in Git.

**In the tests: what each test verifies.** A comment above the test function:

```go
// Verifies: HAMSP-52 AC-2; T-01
func TestSuspendedTenantGets403(t *testing.T) { ... }
```

The comment moves with the test when it is renamed or moved. It is the source of the trace.

**`qa/features/HAMSP-N.yaml`: the verified state of one feature.** Written by `feature-verify` after each run:

```yaml
issue: HAMSP-52
target_stage: alpha
flag: tenant-suspend
plan_approved: 2026-11-12          # gate G-07
checklist:                          # level: basic
  T-01:
    planned: applies — a token of tenant B on a suspended tenant A's record; gateway and ExampleApp
    verified: [apps/gateway/suspend_test.go:TestSuspendedTenantForeignToken]
  T-02:
    planned: does not apply — no new contract
    verified: n/a — the gateway answers 403 with the existing error body
  T-08:
    planned: does not apply — no migration
    verified: n/a — no migration in the diff
acceptance:
  AC-1: [apps/gateway/suspend_test.go:TestSuspendedTenantGets403]
quality_scenarios:
  touched: [QS-03]
  recommended_runs: [QS-03 on kind, k6 noisy tenant]
deviations: []                      # planned "applies" that became n/a, with the owner's approval
runs:
  - date: 2026-11-20
    commit: 1a2b3c4
    stage_checked: alpha
    verdict: ready
    failed: []
    not_run: []
```

**`qa/qs-runs.md`: every load or chaos run.** One row per run: date, commit, environment, QS, result, the issue it was run for, and a link to the report (k6 summary, Chaos Mesh log).

**Linear:** the issue gets the verdict and a link to the QA record, not a copy of it.

CI checks the records: every test named in a `qa/features/` file exists and carries a matching `Verifies:` comment, and every item of the target stage's level has tests or `n/a` with a reason.
