---
name: feature-verify
description: "Check that a HAMSP issue is done: every acceptance criterion and touched quality scenario has a test that runs and passes. Updates QS status in requirements.md and the AC checkboxes in Linear. Use when the user asks to verify an issue (HAMSP-N) or whether a feature is ready."
---

# feature-verify

A feature is ready when every AC-n and every touched QS-xx in the issue has a test that was **run in this session** and **passed**. Reading code is not evidence. A test that exists but was not run counts as not verified.

Input: an issue ID (`HAMSP-N`). The current branch should hold the implementation.

## 1. Load

1. The issue (Linear MCP: `get_issue`): Acceptance criteria, Touched requirements, and the `## Plan` with its Test map. If there is no Test map, build one now from the ACs and the code, and tell the user it was missing.
2. The QS rows the issue touches in `docs/architecture/requirements.md`.
3. The diff of the branch against `master` (`git diff master...HEAD --stat`, then the files you need).

## 2. Check coverage

For each row of the Test map:

1. Find the test in the code. If it is missing, the row fails with "no test".
2. Read the test and check that it checks the behaviour the AC or QS states, not something near it. A cross-tenant test must use two tenants and assert that B's data is absent. A durability test must compare acknowledged writes with stored ones. If it does not, the row fails with "test does not check the criterion".

Also check every Plan step is done (`S1`…`Sn`) and nothing in the diff contradicts an Accepted ADR or a constraint C-xx (for example a multi-tenant table without RLS, a manual cluster change, an AGPL dependency under `/apps`).

## 3. Run

Run the commands from the Test map. Group rows that share a command. Long runs (k6, Chaos Mesh, anything over ~2 minutes) go in the background.

- Record for each row: pass, fail (with the first relevant lines of output), or not run (with the reason, for example "needs the EKS cluster").
- Do not edit code or tests to make them pass during verification. Report the failure; fixing it is a separate step the user decides on.
- `manual:` rows: give the user the steps and ask for the result. Do not mark them passed yourself.

## 4. Record the result

Ask the user before each write:

- **requirements.md:** for each QS-xx whose test ran, set its Status to `Pass (<YYYY-MM-DD>, HAMSP-N)` or `Fail (<YYYY-MM-DD>, HAMSP-N)`. Leave QS rows that did not run unchanged.
- **Linear:** tick the AC checkboxes whose tests passed (`save_issue`). Leave the rest unticked. Never change the issue status; the GitHub integration does that.
- Problems outside the issue's scope become new issues in Triage, linked to this one (see `AGENTS.md`).

## 5. Report

Lead with the verdict: **Ready** (every row passed), or **Not ready** with the count of failing and not-run rows. Then the table:

| Covers | Test | Result | Note |
|---|---|---|---|
| AC-1 | `TestCreateTenant` | pass | |
| QS-01 | `TestCrossTenantRead` | fail | returns tenant B row for `/records/{id}` |
| QS-04 | `k6 + chaos failover` | not run | needs the EKS cluster |

Then the ADR or constraint findings and the Triage issues created. The text of this report is what goes into the end-of-session comment on the issue (`AGENTS.md` step 3).
