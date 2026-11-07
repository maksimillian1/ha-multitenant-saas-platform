# ADR-0011: Features ship in stages backed by gates in a feature file

Date: 2026-11-07

## Status

Accepted

The rules in detail: [Feature stages](../process/feature-stages.md), [Testing](../process/testing.md), [Review](../process/review.md). The feature file format: [Features](../features/README.md).

## Context

Agents write most of the code. The owner cannot read every change, and code nobody has read must not look the same as code the owner stands behind.

What we need:

* a visible level of trust for each part of the code, from "may be thrown away" to "the owner has read it";
* rules for what each level needs, checked by agents and CI wherever a rule can be stated, so the owner reads only what cannot be checked;
* the owner's reviews recorded, so a later change to reviewed code is seen and read as a delta;
* state in the repo, next to the code, reviewed in the same pull request; no gate depends on Linear or another tool.

## Decision

### 1. Four stages

| Stage | What it promises | Checked by |
|---|---|---|
| `proto` | It compiles and does roughly the right thing. May be thrown away. | Agent: build, lint, unit tests. |
| `alpha` | Does what the acceptance criteria say. Contracts and schema may still change. | Agent: `feature-verify` is Ready; the owner approved the plan. |
| `beta` | Does it safely: boundaries, security, end-to-end behaviour are tested. | Agent, every guardrail of the stage; the owner approved the structure. |
| `stable` | The owner has read the code and stands behind it. | Owner. |

The names follow Kubernetes feature gates. A stage is a statement about the quality of a feature's code at a commit, not about which users can reach it.

### 2. One file per feature

A feature is keyed by a kebab-case slug. `docs/features/<slug>.yaml` holds its stage, the code it owns (`code` globs), its acceptance criteria, the QA checklist with the planned and verified tests, the gates and every `feature-verify` run. A generated map lists all features. Tests name what they verify with a `// Verifies: <slug> AC-n; T-xx` comment.

### 3. A stage is backed by gates

Gates G-01 to G-08 ([Feature stages](../process/feature-stages.md), "Gates per stage") say what each stage needs. Each gate has an entry in the feature file, set by `feature-verify` or by the owner. CI rejects a stage whose required gates are not `pass` or `n/a`. A gate whose tooling does not exist yet is reported as *not checked* and does not block.

### 4. The owner reads at fixed points

The owner approves the plan's integration, contract and end-to-end rows before `alpha` (G-07), the structural change before `beta` (G-08) and the code before `stable` (G-06, with the commit it was read up to). Everything else is checked by CI, architecture fitness functions and `feature-verify` ([Review](../process/review.md)).

### 5. Going down is manual

Every pull request gets a comment listing the features whose code it changes, with their stage and the files changed since the last review. Nothing is lowered automatically: the owner moves G-06 to the new commit or lowers the stage.

### 6. Critical features

A feature whose bug leaks data between tenants or loses usage is `critical: true`, set by the owner. Changes to its code get the critical checklist items already at `alpha`, a higher diff coverage bar, and are first in the queue for `stable`.

## Considered options

| Option | Verdict |
|---|---|
| The owner reviews every pull request in full | Rejected. Does not scale with agent output, and gives no record of what was read. |
| Stage as a Linear label or field | Rejected. CI cannot check it, and it drifts from the code. |
| A review map by path (`.github/review-map.yml`) with a separate review ledger (the owner's first draft for HAMSP-45) | Replaced by feature files: one file per feature holds the stage, the code globs and the reviews together. |
| Package markers in `doc.go` (`// Stability: experimental`) | Not now. The `code` globs in the feature file are the source; a marker would be a second one to keep in sync. |

## Consequences

* Every feature costs a feature file and a `feature-verify` run per stage. In return, the trust level of any code is visible in the map.
* Most guardrails the stages rely on do not run yet; their state is in [Guardrails](../process/guardrails.md). Until they run, the gates that need them are *not checked*.
* `HAMSP-23-platform-skeleton` is merged to `master` before its features have a stage; they get one through `feature-verify`.
* A stage does not hide code from tenants: merged code runs wherever it is deployed, whatever its stage.
