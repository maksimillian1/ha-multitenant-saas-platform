---
name: feature-plan
description: "Plan the implementation of a clarified HAMSP feature: check it against docs/ and the ADRs, list the steps, map every acceptance criterion to a test and make the plan pass of the QA checklist. Writes the planned tests and checklist into docs/features/<slug>.yaml and the steps into the Linear issue. Use when the user asks to plan an issue (HAMSP-N) or a feature."
---

# feature-plan

Produce a short plan that a developer or agent can implement and that `feature-verify` can check. Two places:

* **The feature file** `docs/features/<slug>.yaml` (format: `docs/features/README.md`): the code the feature owns, the planned test for each AC and cheap QS, the plan pass of the checklist. This is what `feature-verify` checks against.
* **The Linear issue**, under `## Plan`: the approach, the docs check and the steps. This is the work.

Input: an issue ID (`HAMSP-N`) or a feature slug.

## 1. Check the input

Find the feature file by the slug, or by the issue ID under `links.issues`. It must have `acceptance` with `AC-n` items that have a `text`. If it does not exist or has no ACs, or if the issue's `## Open questions` holds something that blocks the design, stop and suggest `feature-clarify`.

## 2. Load context

1. The issue (Linear MCP: `get_issue`, `list_comments`) for the goal, the decisions and the open questions.
2. `docs/index.md`, then only the subsystem files and ADRs that the issue's References and the file's `requirements` point to.
3. The FR, QS and C rows from `docs/architecture/requirements.md` listed in the file.
4. The other feature files whose `code` the plan will touch, to know whether the change is critical (a feature with `critical: true`) and which `stable` features it changes.
5. The code in the areas the plan will touch: existing structure, naming, test layout and how tests are run (CI workflow, `go test`, k6 scripts). Read enough to place the work, not the whole tree.

## 3. Docs check

Before writing steps, check the approach against the docs. Write the result as a short list, one line per item:

- Each Accepted ADR the feature touches: followed, or how the plan stays inside it.
- Each touched QS-xx: which step or test keeps it true.
- Constraints C-xx that limit the approach (Go, MIT/no AGPL, Terraform + GitOps only, budget).

If the plan needs a decision that no ADR covers (a new technology, a new data store, a change to tenant isolation), do not decide it inside the plan. Tell the user it needs an ADR, offer to draft one in `docs/adr/` with status Proposed, and continue only with the parts that do not depend on it.

A risk or bug found outside the issue's scope becomes a new issue in Triage, linked to this one (see `AGENTS.md`).

## 4. Write the plan

Keep it short: the plan is a map, not the code. Show it to the user and ask before saving.

**The plan is for the next stage** above the file's `stage` (`proto` when it has none). **When that stage is `alpha` or higher, the checklist needs the owner's approval** (gate G-07, `docs/process/review.md`, R-1). Show the integration, contract and end-to-end rows and the touched QS first, separately from the rest of the plan, so they can be reviewed alone. When the owner approves in chat, set `plan_approved: <YYYY-MM-DD>` in the feature file. Implementation does not start before that.

### Into the feature file

```yaml
critical: false                     # true when the feature itself is critical; the owner decides
flag: tenant-suspend                # release flag, if the feature changes behaviour
code:
  - apps/gateway/suspend/**
c4: [apiGateway]                    # optional: elements of docs/architecture/c4/workspace.dsl

acceptance:
  AC-1:
    text: ...                       # from feature-clarify, unchanged
    level: integration
    planned: apps/gateway/suspend/suspend_test.go:TestSuspendedTenantGets403

checklist:                          # next stage alpha → level basic; owner reviews integration, contract, e2e rows
  T-01:
    planned: applies — a token of tenant B on tenant A's record; gateway → ExampleApp → PostgreSQL
  T-02:
    planned: applies — new POST /v1/tenants in docs/contracts/tenant/openapi.yaml
  T-08:
    planned: does not apply — no migration
  T-11:
    planned: unsure — depends on whether the request body gets its own decoder

quality_scenarios:
  touched: [QS-03, QS-18]
  tests:
    QS-03:
      planned: prototype/e2e/ratelimit_test.go:TestNoisyTenantLowLoad
```

Then run `go -C tools/featuremap run .` and commit the file and the map on the feature branch.

### Into the issue

```markdown
## Plan

**Approach:** <2–4 sentences>

**Docs check**
- ADR-0002 (RLS): new table `x` gets RLS and a policy on `app.current_tenant_id`.
- QS-01: covered by the AC-2 integration test.
- C-04: the new Helm values go through ArgoCD.

**Steps**
- [ ] S1: <what> — `<paths>` — AC-1
- [ ] S2: ...

Planned tests and checklist: `docs/features/<slug>.yaml`.
```

Rules:
- **Checklist, plan pass** (`docs/process/testing.md`, sections 4 and 5). Take the level of the next stage (`basic` for `alpha`, `full` for `beta` and `stable`); add the *critical* items when the change is critical (it touches the code of a feature with `critical: true`, or the feature itself is critical). For each item write *applies*, *does not apply* with one line why, or *unsure*. For integration, contract and end-to-end items that apply, one line on the scenario and the components involved. No test names and no unit-level detail here: that is the verify pass. Skip the checklist when the next stage is `proto`.
- **One test may cover several IDs.** Give it once per ID in `planned`; its `// Verifies:` comment lists them all.
- **A feature that was planned for a lower stage before:** keep the approved rows, plan only what the next level adds, and ask for approval of that delta.
- Touched QS-xx: list them. A cheap QS test (integration, low load on kind) gets a `planned` test. Do not plan load or chaos runs; `feature-verify` proposes them from the actual change.
- Every AC-n gets a `planned` test. If something can only be checked by hand, write `manual: <exact steps>` and say why.
- `code` names what this feature owns, as globs from the repo root. Code that belongs to another feature stays in that feature's file; the change to it is listed in the plan.
- Tests come first in the steps that introduce them, or in the same step as the code. Never in a final "add tests" step.
- Steps are small enough for one commit each and name the files they touch.
- Use the test names you intend to create, so `feature-verify` can find them. Update `planned` if names change during implementation.
- Commits follow `AGENTS.md`: `HAMSP-N: <summary>`.

## 5. Report

In chat: the approach in two lines, the checklist rows for owner review, whether the change is critical and which `stable` features it touches, the docs-check findings that matter (conflicts, missing ADRs), the number of steps, and Triage issues created. Do not change the issue status. Implementation follows the steps; `feature-verify` checks the result.
