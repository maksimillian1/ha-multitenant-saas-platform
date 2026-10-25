---
name: feature-plan
description: "Plan the implementation of a clarified HAMSP issue: check it against docs/ and the ADRs, list the steps and map every acceptance criterion to a test. Writes the plan into the Linear issue. Use when the user asks to plan an issue (HAMSP-N)."
---

# feature-plan

Produce a short plan that a developer or agent can implement and that `feature-verify` can check. The plan lives in the issue description under `## Plan`. Do not create plan files in the repo.

Input: an issue ID (`HAMSP-N`).

## 1. Check the input

Read the issue (Linear MCP: `get_issue`, `list_comments`). It must have `## Acceptance criteria` with `AC-n` items and `## Touched requirements`. If it does not, or if `## Open questions` holds something that blocks the design, stop and suggest `feature-clarify`.

## 2. Load context

1. `docs/index.md`, then only the subsystem files and ADRs that the issue's References and Touched requirements point to.
2. The FR, QS and C rows from `docs/architecture/requirements.md` listed in the issue.
3. The code in the areas the plan will touch: existing structure, naming, test layout and how tests are run (`Makefile`, CI workflow, `go test`, k6 scripts). Read enough to place the work, not the whole tree.

## 3. Docs check

Before writing steps, check the approach against the docs. Write the result as a short list, one line per item:

- Each Accepted ADR the feature touches: followed, or how the plan stays inside it.
- Each touched QS-xx: which step or test keeps it true.
- Constraints C-xx that limit the approach (Go, MIT/no AGPL, Terraform + GitOps only, budget).

If the plan needs a decision that no ADR covers (a new technology, a new data store, a change to tenant isolation), do not decide it inside the plan. Tell the user it needs an ADR, offer to draft one in `docs/adr/` with status Proposed, and continue only with the parts that do not depend on it.

A risk or bug found outside the issue's scope becomes a new issue in Triage, linked to this one (see `AGENTS.md`).

## 4. Write the plan

Keep it short: the plan is a map, not the code. Show it to the user and ask before saving it into the issue (`save_issue`). Append it after the existing sections:

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

**Test map**
| Covers | Test | Level | Command |
|---|---|---|---|
| AC-1 | `apps/api/tenant_test.go: TestCreateTenant` | integration | `go test ./apps/api/...` |
| QS-01 | `apps/api/isolation_test.go: TestCrossTenantRead` | integration | `go test -run CrossTenant ./apps/api/...` |
```

Rules:
- Every AC-n and every touched QS-xx appears in the Test map. A row without a test is not allowed. If something can only be checked by hand, write `manual: <exact steps>` in the Test column and say why.
- Tests come first in the steps that introduce them, or in the same step as the code. Never in a final "add tests" step.
- Steps are small enough for one commit each and name the files they touch.
- Use the test names you intend to create, so `feature-verify` can find them. Update the Test map if names change during implementation.
- Commits follow `AGENTS.md`: `HAMSP-N: <summary>`.

## 5. Report

In chat: the approach in two lines, the docs-check findings that matter (conflicts, missing ADRs), the number of steps, and Triage issues created. Do not change the issue status. Implementation follows the steps; `feature-verify` checks the result.
