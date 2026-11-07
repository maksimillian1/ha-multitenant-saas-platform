# Development process

How code gets from an issue to `stable`, and what checks it on the way. The decision behind it: [ADR-0011](../adr/0011-feature-stages.md).

| File | What it answers | Read it when |
|---|---|---|
| [Feature stages](feature-stages.md) | The stages `proto` to `stable`, the gates each stage needs, how a feature moves between them. | First. Every other file is detail behind a gate here. |
| [Testing](testing.md) | Where checks run (L1–L3), the QA checklist, diff coverage, quality scenarios, where results are kept. | You plan or verify a feature (gates G-01, G-04, G-05). |
| [Review](review.md) | What the owner reads (R-1 to R-5) and the architecture fitness functions that check the rest. | You prepare something for the owner (gates G-06, G-07, G-08). |
| [Guardrails](guardrails.md) | Which of the checks above run today, and which issue adds the missing ones. No rules of its own. | You want to know whether a check can be relied on. |

The rules live in the first three files. `guardrails.md` only says whether each rule is enforced yet; when a check starts running, its row changes there, not the rule.

The state of each feature is not here: it is in [docs/features](../features/README.md).
