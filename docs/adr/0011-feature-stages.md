# ADR-0011: Features ship in stages backed by gates in a feature file

Date: 2026-11-07

## Status

Accepted

## Context

Agents write most of the code. The owner cannot read every change, and code nobody has read must not look the same as code the owner stands behind.

What we need:

* a visible level of trust for each part of the code, from "may be thrown away" to "the owner has read it";
* rules for what each level needs, checked by agents and CI wherever a rule can be stated, so the owner reads only what cannot be checked;
* the owner's reviews recorded, so a later change to reviewed code is seen and read as a delta;
* state in the repo, next to the code, reviewed in the same pull request; no gate depends on Linear or another tool.

## Decision

1. **Four stages:** `proto`, `alpha`, `beta`, `stable`, named after Kubernetes feature gates. A stage is a statement about the quality of a feature's code at a commit. `stable` means the owner has read the code.
2. **One file per feature**, keyed by a slug: `docs/features/<slug>.yaml` holds the stage, the code the feature owns and everything that backs the stage. A map of all features is generated from these files.
3. **A stage is backed by gates.** Each stage requires a set of gates; each gate is set by `feature-verify` or by the owner, and CI rejects a stage its gates do not back.
4. **The owner reads at fixed points:** the plan before `alpha`, the structure before `beta`, the code before `stable`. Agents, CI and architecture fitness functions check the rest.
5. **Nothing goes down automatically.** A change to reviewed code is shown to the owner, who accepts it or lowers the stage.
6. **Critical features** (a bug leaks data between tenants or loses usage) get stricter checks from `alpha` on and go to `stable` first.

The rules: [Feature stages](../process/feature-stages.md) (stages, gates, critical features), [Testing](../process/testing.md), [Review](../process/review.md), [Features](../features/README.md) (the file format).

## Considered options

| Option | Verdict |
|---|---|
| The owner reviews every pull request in full | Rejected. Does not scale with agent output, and gives no record of what was read. |
| Stage as a Linear label or field | Rejected. CI cannot check it, and it drifts from the code. |
| A review map by path (`.github/review-map.yml`) with a separate review ledger (the owner's first draft for HAMSP-45) | Replaced by feature files: one file per feature holds the stage, the code globs and the reviews together. |
| Package markers in `doc.go` (`// Stability: experimental`) | Not now. The `code` globs in the feature file are the source; a marker would be a second one to keep in sync. |

## Consequences

* Every feature costs a feature file and a `feature-verify` run per stage. In return, the trust level of any code is visible in the map.
* Many guardrails the stages rely on do not run yet ([Guardrails](../process/guardrails.md)); until they do, the gates that need them are *not checked*.
* A stage does not hide code from tenants: merged code runs wherever it is deployed, whatever its stage.
