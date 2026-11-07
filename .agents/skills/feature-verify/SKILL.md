---
name: feature-verify
description: "Check that a HAMSP feature is done for its target stage: every acceptance criterion and every QA checklist item of that stage has a test that runs and passes. Writes the result into docs/features/<slug>.yaml, regenerates the feature map and updates QS status in requirements.md. Use when the user asks to verify a feature or an issue (HAMSP-N), or whether a feature is ready."
---

# feature-verify

A feature is ready for its target stage when every AC-n, every checklist item `T-xx` of that stage's level and every cheap QS-xx test has a test that was **run in this session** and **passed**, or, for a checklist item, `n/a` with a reason. Reading code is not evidence. A test that exists but was not run counts as not verified.

The rules are in `docs/process/feature-stages.md` (gates G-01 to G-08) and `docs/process/testing.md` (checklist, flags, QS runs). The feature file format is in `docs/features/README.md`.

Input: a feature slug, or an issue ID (`HAMSP-N`) whose feature is found under `links.issues` in `docs/features/*.yaml`. The current branch should hold the implementation.

## 1. Load

1. The feature file `docs/features/<slug>.yaml`: `target` (the stage to check), `acceptance` with the `planned` tests, the `checklist` plan pass, `quality_scenarios`, `plan_approved`, `structure_approved`, earlier `runs`. If the file has no `target`, ask the user for it. If ACs have no `planned` test, build the mapping now from the ACs and the code, and tell the user the plan was missing it.
2. The QS rows the feature touches in `docs/architecture/requirements.md`.
3. The diff of the branch against the branch it merges into: the integration branch for a sub-issue (`AGENTS.md`), otherwise `master` (`git diff <base>...HEAD --stat`, then the files you need).
4. The other feature files whose `code` the diff touches. The change is critical when it touches the code of a feature with `critical: true`, or the feature itself is critical. Note every `stable` feature it changes.

The Linear issue is not needed. Read it only for context the file does not have.

## 2. Check coverage

For each AC and each cheap QS test:

1. Find the test in the code. If it is missing, the row fails with "no test".
2. Read the test and check that it checks the behaviour the AC or QS states, not something near it. A cross-tenant test must use two tenants and assert that B's data is absent. A durability test must compare acknowledged writes with stored ones. If it does not, the row fails with "test does not check the criterion".
3. Check its `// Verifies:` comment names the feature's slug and the ID (`// Verifies: tenant-suspend AC-1; T-01`). A missing comment is a failing row; CI rejects the record without it.

### Checklist

This is the verify pass of the checklist (`docs/process/testing.md`, sections 4 and 5). Start from the `planned` entries; take the level of the target stage (`basic` for `alpha`, `full` for `beta` and `stable`) and add the items marked *critical* when the change is critical. For each item:

1. Decide whether it applies, from its "Applies when" column and the actual diff. If it does not, write `n/a` with a one-line reason that names what is absent ("no migration in the diff"). "Not needed" is not a reason.
2. If it applies, find the tests by their `// Verifies:` comments and read them as in steps 1 and 2 above. Record the test names and the cases they cover. An item that applies and has no test fails with "no test".
3. **Ratchet.** An item planned as *applies* under `plan_approved` cannot become `n/a` here. Report it as a **deviation** and treat it as failing until the owner approves the change; the approval goes into `deviations` with its date.
4. Items or cases that the diff shows but the plan missed are added, marked as *added by verify*. Missing tests for them fail like any other; do not write them.
5. When the target stage is `alpha` or higher and the file has no `plan_approved`, gate G-07 fails.

One test may cover several IDs; it is listed under each of them.

### Gates outside the checklist

- **G-01, flag off:** a feature that changes behaviour has a release flag in the registry that names this feature, and the suite passes with it off. At `alpha` and above, the AC tests also run with it on.
- **G-03, contracts:** every schema the feature adds or changes is in `docs/contracts/` and its examples pass. Its contract rows are under `plan_approved`; a schema the plan did not mention is a deviation and waits for the owner.
- **G-05, quality scenarios:** propose load and chaos runs for the touched QS from the actual change, using the table in `docs/process/testing.md`, section 7. Load and chaos runs never count toward the verdict: report each as run (with its result) or *not run*.
- **G-08, structure** (target `beta` or `stable`): the architecture fitness functions pass, and the change to the architecture rules since the feature started is listed for the owner (`docs/process/review.md`, R-4). Without `structure_approved` the gate fails. A rule change that allows a new dependency on a critical feature is listed at every stage.
- **Stable features touched:** list every `stable` feature whose code the diff changes. It is shown as `beta` in the map until the owner adds a `reviews` entry; this does not fail the verdict for the feature being verified.
- A gate whose tooling does not exist yet is *not checked*: say so in the report, and do not count it as passed.

### Plan and docs

Check every Plan step in the issue is done (`S1`…`Sn`) and nothing in the diff contradicts an Accepted ADR or a constraint C-xx (for example a multi-tenant table without RLS, a manual cluster change, an AGPL dependency under `/apps`).

## 3. Run

Run the planned tests and the tests of the checklist items that apply. Derive the command from the test path (`go test -run '^TestX$' ./path/...` in the module of that path); group tests that share a package. Long runs (k6, Chaos Mesh, anything over ~2 minutes) go in the background.

- Record for each row: pass, fail (with the first relevant lines of output), or not run (with the reason, for example "needs the EKS cluster").
- Do not edit code or tests to make them pass during verification. Report the failure; fixing it is a separate step the user decides on.
- `manual:` rows: give the user the steps and ask for the result. Do not mark them passed yourself.

## 4. Record the result

Ask the user before writing:

- **Feature file:** the `verified` entries of each AC, checklist item and QS test; `recommended_runs`; a new entry under `runs` with the date, the commit, the stage checked, the verdict and the failed and not-checked IDs. Earlier runs stay. When the verdict is Ready, set `stage` to the target stage.
- **Map:** run `go -C tools/featuremap run .` and include `docs/features/README.md` in the same commit.
- **QS runs:** for each load or chaos run that happened, add a row to `docs/features/qs-runs.md`.
- **requirements.md:** for each QS-xx whose test ran, set its Status to `Pass (<YYYY-MM-DD>, <slug>)` or `Fail (<YYYY-MM-DD>, <slug>)`. Leave QS rows that did not run unchanged.
- **Linear (informational):** a comment on the issue with the verdict and a link to the feature file. Change the issue status only as `AGENTS.md` step 5 allows.
- Problems outside the feature's scope become new issues in Triage, linked to the issue (see `AGENTS.md`).

## 5. Report

Lead with the verdict for the target stage: **Ready for `<stage>`** (every required row passed or is `n/a` with a reason), or **Not ready** with the count of failing and not-run rows. Load and chaos runs do not change the verdict.

Then, before the table, the part the owner reads (`docs/process/review.md`, R-3): **deviations from the plan**, **items added by verify** and **`stable` features touched**, one line each. Say "none" when there are none.

Then the table:

| Covers | Test | Result | Note |
|---|---|---|---|
| AC-1 | `TestCreateTenant` | pass | |
| T-01 | `TestCrossTenantRead` | fail | returns tenant B row for `/records/{id}` |
| T-08 | | n/a | no migration in the diff |
| G-03 | `docs/contracts/tenant/openapi.yaml` | pass | under the approved plan |
| QS-04 | `k6 + chaos failover` | not run | recommended; needs the EKS cluster |
| T-12 | `TestCrossTenantRead` with RLS dropped | not checked | the canary run is not set up yet |
| T-06 | `TestCreateTenantZitadelDown` | fail | added by verify: the handler calls Zitadel, the plan missed it |

Then the ADR or constraint findings and the Triage issues created. The text of this report is what goes into the end-of-session comment on the issue (`AGENTS.md` step 3).
