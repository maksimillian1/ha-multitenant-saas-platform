# Features

The state of every feature: its stage, the gates behind that stage, what it promises and what proves it. One file per feature, `<slug>.yaml`.

**→ [Feature map](map.md)**: every feature with its stage and gates. Generated from the files in this folder by `tools/featuremap`; run `go -C tools/featuremap run .` after changing a feature file.

The rules behind the fields are in [Feature stages](../process/feature-stages.md), [Testing](../process/testing.md) and [Review](../process/review.md).

## A feature is its slug

A feature is a capability of the platform, such as `tenant-suspend` or `gateway-auth`, named by a short kebab-case slug. The slug is the file name and the key everything else uses: tests (`// Verifies: tenant-suspend AC-2; T-01`), release flags, when a feature has one, the QS runs log and the skills.

Linear issues are the work on a feature. The file lists them under `links`, and each issue links back to the file. The links are for people. No tool and no gate reads Linear: if Linear is gone, the state of every feature is still here.

## Stage and gates

A feature's `stage` is backed by its `gates` ([Feature stages](../process/feature-stages.md), "Where the stage is kept"). A feature without a stage has not been brought to any stage yet.

Each gate entry says who set it:

* `by: verify`: `feature-verify` ran the checks and recorded the result;
* `by: owner`: the owner marked it, either an approval only the owner can give (G-06, G-07, G-08) or a gate the owner decided to accept without a run.

## CI

`.github/workflows/features.yml` has two jobs.

**`map`, on every pull request and push.** Runs the generator's own tests, then `featuremap -check`, which fails when:

| Check | Fails when |
|---|---|
| The file can be read | It is not valid YAML, or it has a field this page does not list (a typo such as `stgae:` fails instead of being ignored). |
| Values | `stage` is not `proto`, `alpha`, `beta` or `stable`; IDs are not `AC-n`, `T-01`…`T-12`, `QS-xx`, `FR-xx`, `C-xx`, `HAMSP-N`; dates are not `YYYY-MM-DD`; commits are not hashes; a `verified` entry is neither a list of tests nor `n/a — <reason>`. |
| Gates | A gate is not `G-01`…`G-08`; status is not `pass`, `fail`, `n/a` or `not-checked`; `by` is not `verify` or `owner`; `n/a` without a `note`; a G-04 pass without `level: basic` or `full`; a G-06 pass without the commit reviewed up to. |
| Rules between fields | `feature` differs from the file name; **`stage` is not backed by its gates** (a gate the stage requires is missing, failed or not checked, or G-04 has a lower level than the stage needs); `stage` is set but `code` is empty; an acceptance criterion is `n/a`. |
| The repository | A `code` glob of a feature with a stage matches no file; a test in a `verified` entry does not exist, or has no `// Verifies:` comment with the feature's slug and the ID. |
| The map | `map.md` differs from what the files produce. |

It does not run the feature's tests and does not judge whether a feature is ready: that is `feature-verify`, recorded in `runs`.

**`touched`, on pull requests.** Comments on the pull request with a table of the features whose `code` the pull request changes: stage, critical mark, files changed, the last review and the files changed since it. The comment is updated on every push. What the owner does with it: [Feature stages](../process/feature-stages.md), "Going down".

## File format

```yaml
feature: tenant-suspend             # the slug, equal to the file name
title: Suspend a tenant
stage: alpha                        # backed by the gates below; omitted before any stage
critical: false                     # true: a bug here leaks data between tenants or loses usage
flag: tenant-suspend                # its release flag, if it has one
code:                               # the code this feature owns, as globs from the repo root
  - apps/gateway/suspend/**
c4: [apiGateway]                    # optional: element names in docs/architecture/c4/workspace.dsl
links:                              # informational only
  issues: [HAMSP-52]
requirements: [FR-01, QS-03]        # the FR, QS and C items the feature touches

acceptance:                         # gate G-02; written by feature-clarify
  AC-1:
    text: A request of a suspended tenant gets 403 at the API Gateway.
    level: integration
    planned: apps/gateway/suspend/suspend_test.go:TestSuspendedTenantGets403   # feature-plan
    verified: [apps/gateway/suspend/suspend_test.go:TestSuspendedTenantGets403] # feature-verify

checklist:                          # gate G-04; Testing, sections 4 and 5
  T-01:
    planned: applies — a token of tenant B on a suspended tenant A's record; gateway and ExampleApp
    verified: [apps/gateway/suspend/suspend_test.go:TestSuspendedTenantForeignToken]
  T-08:
    planned: does not apply — no migration
    verified: n/a — no migration in the diff

quality_scenarios:                  # gate G-05; Testing, section 7
  touched: [QS-03]
  tests:                            # cheap QS tests, counted like ACs
    QS-03:
      planned: prototype/e2e/ratelimit_test.go:TestNoisyTenantLowLoad
  recommended_runs: [QS-03 on kind, k6 noisy tenant]

gates:                              # Feature stages, "Gates per stage"; a missing gate is not recorded
  G-01: {status: pass, by: verify, date: 2026-11-20, commit: 1a2b3c4d}
  G-02: {status: pass, by: verify, date: 2026-11-20, commit: 1a2b3c4d}
  G-03: {status: n/a, by: verify, date: 2026-11-20, note: no new schema}
  G-04: {status: pass, by: verify, date: 2026-11-20, level: basic}   # basic | full
  G-05: {status: not-checked, by: verify, date: 2026-11-20, note: QS-03 run recommended}
  G-07: {status: pass, by: owner, date: 2026-11-12}                 # plan approved
  # G-06: {status: pass, by: owner, date: ..., commit: ...}         # code read up to this commit
  # G-08: {status: pass, by: owner, date: ...}                      # structure approved

deviations: []                      # approved "applies" that became n/a, each with the owner's approval date
runs:                               # one entry per feature-verify run, newest last
  - date: 2026-11-20
    commit: 1a2b3c4d
    stage_checked: alpha
    verdict: ready                  # ready | not-ready
    failed: []
    not_checked: [G-01]
```

### Who writes what

| Field | Written by |
|---|---|
| `feature`, `title`, `acceptance.*.text`, `requirements`, `links` | `feature-clarify` |
| `code`, `flag`, `critical`, `c4`, the `planned` entries, `quality_scenarios.touched` | `feature-plan` |
| Gates `by: owner` (G-06, G-07, G-08, and any gate the owner accepts by hand), `deviations` approvals, setting or lowering `stage` by hand | The owner, in chat or in the file; an agent may write the line on the owner's word. |
| Gates `by: verify`, `stage` when the gates back it, the `verified` entries, `recommended_runs`, `runs` | `feature-verify` |

`code` globs may overlap: a file can belong to two features, and a change to it counts for both. A file that belongs to no feature is allowed.

## Load and chaos runs

[qs-runs.md](qs-runs.md): one row per run, never required.
