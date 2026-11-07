---
name: feature-clarify
description: "Interview the user about a HAMSP feature before planning. Reads the Linear issue, the feature file and docs/, asks up to 5 targeted questions, then writes the target stage and testable acceptance criteria into docs/features/<slug>.yaml. Use when the user says clarify, refine or 'ask me about' an issue (HAMSP-N) or a feature."
---

# feature-clarify

Turn a Linear issue into a spec that `feature-plan` can plan and `feature-verify` can test. The spec lives in the feature file `docs/features/<slug>.yaml` (format: `docs/features/README.md`). The issue keeps the work: goal, decisions, open questions and a link to the file.

Input: an issue ID (`HAMSP-N`) or a feature slug. If neither is given, ask for one.

## 1. Load context (read only what you need)

1. The issue: description and comments (Linear MCP: `get_issue`, `list_comments`).
2. The feature file. Find it by the slug, or by the issue ID under `links.issues` in `docs/features/*.yaml`. If there is none, this is a new feature: propose a slug (kebab-case, naming the capability, not the issue, such as `tenant-suspend`) and confirm it with the user. If the issue works on an existing feature, its new ACs are added to that file.
3. `docs/index.md`. Use it to pick the affected subsystem files in `docs/architecture/`, `docs/contracts/` and `docs/ops/`. Read only those.
4. `docs/architecture/requirements.md`: find the FR, QS and C items the feature touches.
5. The ADRs in `docs/adr/` that the issue or the subsystem files reference. An **Accepted** ADR overrides every other doc. A **Proposed** ADR is input, not law.

Do not read code at this step unless the issue is about changing existing code.

## 2. Find the gaps

Check the issue against this list. For each item, decide whether it is answered by the issue, by the docs, or not at all. Only "not at all" items become questions.

| Area | What must be clear |
|---|---|
| Target stage | `proto`, `alpha` or `beta` (`docs/process/feature-stages.md`). Always ask when the file has no `target`; recommend `proto` when the feature depends on an open decision. |
| Scope | Which actor (platform admin, tenant admin, end user, API client) and which FR-xx. What is out of scope. |
| Tenant isolation | Where `tenant_id` comes from. RLS on every new multi-tenant table. What a cross-tenant attempt returns. Network policy for new workloads. |
| Data & durability | What is written, when a write counts as acknowledged, the idempotency or dedup key. |
| Load path | Synchronous API or Kafka. Rate limits. Behaviour at 10× load. |
| Failure modes | Database, Kafka or an external dependency is down: what the caller gets, what retries, what timeouts. |
| Contracts | Request, response or event shape. Whether it goes into `docs/contracts/`. |
| Operations | Terraform/GitOps changes (C-04), config and secrets, cost against C-02, arm64 (C-06). |
| Verifiability | Each criterion is observable and has a test level: unit, integration, k6, Chaos Mesh. |

Skip any area that does not apply to this feature.

## 3. Ask

- At most **5 questions** per run, **one at a time**, the highest-impact one first (isolation and data loss before naming).
- Each question has 2–4 options, your recommended option first and marked, with one line on its consequence. The user can always answer in their own words.
- Do not ask what the docs already answer. Cite the doc instead.
- If an answer contradicts an Accepted ADR or a constraint (C-xx), stop and say so. Offer two ways out: change the answer, or open a Triage issue to revisit the ADR.
- A problem outside this issue's scope becomes a new issue in Triage, linked to this one, with no priority (see `AGENTS.md`). Do not widen the current issue.

Stop early when the remaining gaps would not change the design or the tests.

## 4. Write the spec

Show the user both changes and ask before saving.

**The feature file** (create it from the format in `docs/features/README.md`, or update it):

```yaml
feature: tenant-suspend
title: Suspend a tenant
target: alpha
links:
  issues: [HAMSP-52]
requirements: [FR-01, QS-03]
acceptance:
  AC-1:
    text: When a platform admin suspends tenant A, every API request of tenant A gets 403 at the API Gateway.
    level: integration
```

Fill only these fields. `code`, `planned` and the checklist come from `feature-plan`; `stage`, `verified` and `runs` from `feature-verify`. Then run `go -C tools/featuremap run .` so the map shows the new feature, and commit both on the feature branch (`HAMSP-N: <summary>`).

**The issue** (`save_issue`). Keep the existing text and wording where it is still right:

```markdown
## Goal
<one or two sentences>

**Feature:** [`<slug>`](<link to docs/features/<slug>.yaml on the branch>), target `<stage>`. Acceptance criteria and test status are in the file.

## Touched requirements
FR-xx, QS-xx, C-xx — one line each on how this feature touches it.

## Out of scope
...

## Decisions
- <date>: <question> → <answer> (<who decided>)

## Open questions
<only what was deliberately left open, or "None">

## References
* `docs/...`
```

Rules for acceptance criteria:
- One observable behaviour per AC, written so a test can pass or fail on it. `level` is `unit`, `integration`, `contract`, `e2e`, `k6`, `chaos` or `manual`.
- Use numbers where the docs have them (status codes, latency, counts). If a number is `TODO` in `requirements.md`, list it under Open questions rather than inventing it.
- Each touched QS-xx either gets an AC or an explicit note in Touched requirements on why the feature does not need to prove it.
- IDs belong to the feature, not to the issue. Never renumber existing ACs; a later issue adds new ones at the end. An AC that no longer holds is removed only with the user's approval, and the removal is recorded under Decisions.
- Changing `target` clears `plan_approved` (`docs/process/feature-stages.md`, "Raising the target").

## 5. Report

In chat: the feature slug and target stage, the questions asked and their answers, the ACs added or changed, and any Triage issues created. Do not change the issue status. The next step is `feature-plan`.
