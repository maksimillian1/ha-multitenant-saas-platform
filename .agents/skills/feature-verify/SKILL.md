---
name: feature-verify
description: "Check that a HAMSP issue is done for its target stage: every acceptance criterion and every QA checklist item of that stage has a test that runs and passes. Writes the QA record in qa/features/, updates QS status in requirements.md and the AC checkboxes in Linear. Use when the user asks to verify an issue (HAMSP-N) or whether a feature is ready."
---

# feature-verify

A feature is ready for its target stage when every AC-n, every checklist item `T-xx` of that stage's level and every cheap QS-xx test has a test that was **run in this session** and **passed**, or, for a checklist item, `n/a` with a reason. Reading code is not evidence. A test that exists but was not run counts as not verified.

The rules are in `docs/process/feature-stages.md` (gates G-01 to G-06) and `docs/process/testing.md` (checklist, flags, QS runs, QA records).

Input: an issue ID (`HAMSP-N`). The current branch should hold the implementation.

## 1. Load

1. The issue (Linear MCP: `get_issue`): its `stage:*` label (the target stage), Acceptance criteria, Touched requirements, and the `## Plan` with its Test map. If the issue has no `stage:*` label, ask the user for the target stage. If there is no Test map, build one now from the ACs and the code, and tell the user it was missing.
2. The QS rows the issue touches in `docs/architecture/requirements.md`.
3. The diff of the branch against `master` (`git diff master...HEAD --stat`, then the files you need).
4. The feature's QA record `qa/features/HAMSP-N.yaml`, if it exists, and the review map, to see whether the change touches a `critical` zone.

## 2. Check coverage

For each row of the Test map:

1. Find the test in the code. If it is missing, the row fails with "no test".
2. Read the test and check that it checks the behaviour the AC or QS states, not something near it. A cross-tenant test must use two tenants and assert that B's data is absent. A durability test must compare acknowledged writes with stored ones. If it does not, the row fails with "test does not check the criterion".

### Checklist

This is the verify pass of the checklist (`docs/process/testing.md`, sections 4 and 5). Start from the plan pass in the issue's `## Plan`; take the level of the target stage (`basic` for `alpha`, `full` for `beta` and `stable`) and add the items marked *critical* when the change touches a `critical` zone. For each item:

1. Decide whether it applies, from its "Applies when" column and the actual diff. If it does not, write `n/a` with a one-line reason that names what is absent ("no migration in the diff"). "Not needed" is not a reason.
2. If it applies, find the tests by their `// Verifies:` comments and read them as in steps 1 and 2 above. Record the test names and the cases they cover. An item that applies and has no test fails with "no test".
3. **Ratchet.** An item the plan marked *applies* and the owner approved cannot become `n/a` here. Report it as a **deviation** and treat it as failing until the owner approves the change.
4. Items or cases that the diff shows but the plan missed are added, marked as *added by verify*. Missing tests for them fail like any other; do not write them.
5. When the target stage is `alpha` or higher and the issue has no `**Checklist approved:**` line, gate G-07 fails.

### Gates outside the checklist

- **G-01, flag off:** a feature that changes behaviour has a release flag in the registry, and the suite passes with it off. At `alpha` and above, the AC tests also run with it on.
- **G-03, contracts:** every schema the feature adds or changes is in `docs/contracts/`, and its change was approved by the owner (code owners). An unapproved schema change fails the gate.
- **G-05, quality scenarios:** propose load and chaos runs for the touched QS from the actual change, using the table in `docs/process/testing.md`, section 7. Cheap QS tests (integration, low load on kind) are rows of the Test map and count normally. Load and chaos runs never count toward the verdict: report each as run (with its result) or *not run*.
- **G-08, structure** (target `beta` or `stable`): the architecture fitness functions pass, and the change to the architecture rules since the feature started is listed for the owner (`docs/process/review.md`, R-4). Without the owner's approval the gate fails.
- A gate whose tooling does not exist yet is *not checked*: say so in the report, and do not count it as passed.

### Plan and docs

Check every Plan step is done (`S1`…`Sn`) and nothing in the diff contradicts an Accepted ADR or a constraint C-xx (for example a multi-tenant table without RLS, a manual cluster change, an AGPL dependency under `/apps`).

## 3. Run

Run the commands from the Test map and of the checklist items that apply. Group rows that share a command. Long runs (k6, Chaos Mesh, anything over ~2 minutes) go in the background.

- Record for each row: pass, fail (with the first relevant lines of output), or not run (with the reason, for example "needs the EKS cluster").
- Do not edit code or tests to make them pass during verification. Report the failure; fixing it is a separate step the user decides on.
- `manual:` rows: give the user the steps and ask for the result. Do not mark them passed yourself.

## 4. Record the result

Ask the user before each write:

- **QA record:** write `qa/features/HAMSP-N.yaml` in the format of `docs/process/testing.md`, section 8: each checklist item with its `planned` and `verified` entries, the AC map, the deviations, the touched QS and the proposed runs, and a new entry under `runs` with the date, the commit, the stage checked, the verdict and the failed and not-run items. Earlier runs stay.
- **QS runs:** for each load or chaos run that happened, add a row to `qa/qs-runs.md`.
- **requirements.md:** for each QS-xx whose test ran, set its Status to `Pass (<YYYY-MM-DD>, HAMSP-N)` or `Fail (<YYYY-MM-DD>, HAMSP-N)`. Leave QS rows that did not run unchanged.
- **Linear:** tick the AC checkboxes whose tests passed (`save_issue`). Leave the rest unticked. Change the issue status only as `AGENTS.md` step 5 allows: a Ready sub-issue merged into its integration branch may be set to Done.
- Problems outside the issue's scope become new issues in Triage, linked to this one (see `AGENTS.md`).

## 5. Report

Lead with the verdict for the target stage: **Ready for `<stage>`** (every required row passed or is `n/a` with a reason), or **Not ready** with the count of failing and not-run rows. Load and chaos runs do not change the verdict.

Then, before the table, the part the owner reads (`docs/process/review.md`, R-3): **deviations from the plan** and **items added by verify**, one line each. Say "none" when there are none.

Then the table:

| Covers | Test | Result | Note |
|---|---|---|---|
| AC-1 | `TestCreateTenant` | pass | |
| T-01 | `TestCrossTenantRead` | fail | returns tenant B row for `/records/{id}` |
| T-08 | | n/a | no migration in the diff |
| G-03 | `docs/contracts/tenant/openapi.yaml` | pass | approved by the owner |
| QS-04 | `k6 + chaos failover` | not run | recommended; needs the EKS cluster |
| T-12 | `TestCrossTenantRead` with RLS dropped | not checked | the canary run is not set up yet |
| T-06 | `TestCreateTenantZitadelDown` | fail | added by verify: the handler calls Zitadel, the plan missed it |

Then the ADR or constraint findings and the Triage issues created. The text of this report is what goes into the end-of-session comment on the issue (`AGENTS.md` step 3).
