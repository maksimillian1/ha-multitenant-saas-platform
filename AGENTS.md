# AGENTS.md

## Linear workflow

Tasks live in Linear: team key `HAMSP`, issue IDs look like `HAMSP-42`. Use the Linear MCP server to read and write them.

1. **Read the issue first.** Before any work, fetch the issue and read its description, acceptance criteria and comments. If the request has no issue ID, ask for one (or whether to create one) before starting.
2. **Put the issue ID in the branch, commits and PR.** Linear's GitHub integration links the branch and PR to the issue by this ID.
   * Branch: `HAMSP-<N>-<short-slug>`, e.g. `HAMSP-42-rls-tenant-context`.
   * Commit: `HAMSP-<N>: <summary>`.
   * PR title starts with `HAMSP-<N>`.
3. **Comment on the issue at the end of the session.** One comment: what was done, what was not done or not verified, the PR link, and any new issues you created.
4. **New problems go to Triage.** A bug, risk or open question outside the issue's scope becomes a new issue in Triage, linked to the current one. Do not fix it in the current branch, do not put it in Backlog, do not set its priority.
5. **Issue status.** The GitHub integration moves issues to In Progress and Done. One exception: a sub-issue that is merged into its integration branch (e.g. `HAMSP-23-platform-skeleton`) and that `feature-verify` reports Ready may be set to Done. An issue that is Not ready stays open. Never set Canceled yourself.

## Feature workflow

The rules come from `docs/`: start at `docs/index.md`, read `docs/architecture/requirements.md` (FR, QS, C), and treat Accepted ADRs in `docs/adr/` as overriding everything else.

A feature is keyed by its slug. Its file `docs/features/<slug>.yaml` holds the verified stage, the acceptance criteria, the planned and verified tests, the checklist, the owner's approvals and every verify run (`docs/features/README.md`). The Linear issue holds the work: goal, discussion, decisions and plan steps, with a link to the feature file. Nothing reads feature state from Linear.

1. `feature-clarify HAMSP-N`: asks up to 5 questions, creates or updates the feature file with its acceptance criteria (AC-n), and links it from the issue.
2. `feature-plan HAMSP-N`: checks the approach against the docs, writes the planned tests and checklist into the feature file and the steps into the issue.
3. Implement the steps.
4. `feature-verify <slug or HAMSP-N>`: runs the planned tests. The feature is ready only when every AC, checklist item of its level and touched QS has a test that ran and passed, or `n/a` with a reason. Writes the result into the feature file and regenerates the map.

The skills are in `.agents/skills/`; `.claude/skills` links to the same folder.

## Guardrails

These rules hold while you write code. Checks run at commit (`lefthook.yml`, and for Claude Code also `.claude/hooks/pre-commit.sh`) and in CI. CI is the only layer that enforces them (`docs/process/testing.md`, section 1).

1. **Do not weaken a check to get green.** Disabling or relaxing a linter, a `//nolint`, a type option, a coverage gate or a CI job needs its own issue. A `//nolint` names the linter and the reason: `//nolint:gosec // G704: configured base URL`. A fake secret in a test carries `// gitleaks:allow`.
2. **Do not change a test to make it pass.** A failing test is either a bug in the code or a test that no longer matches an agreed acceptance criterion. Changing the criterion is the owner's decision.
3. **Read the feature file first.** Before changing code, find the feature that owns it (`code` in `docs/features/*.yaml`) and read its acceptance criteria and checklist.
4. **Critical code: announce, then test, then code.** A change to the code of a critical feature (`critical: true`; `docs/process/feature-stages.md`, "Critical features") is announced to the owner before you start. Its boundary tests come before the code: tenant boundaries (T-01), authorization (T-04) and the control canary (T-12) from `docs/process/testing.md`, section 4.
5. **Check before you report done.** Before you say the work is done, run lint and the tests of the touched packages, and report what ran and what failed:
   * `golangci-lint run --config ../../.golangci.yml ./...` in each touched app under `apps/`;
   * `go test ./<touched packages>/...`; database tests need `TEST_DATABASE_URL` (`apps/usage/internal/testpg`), and a skipped test is not a pass;
   * `npm run lint && npm test` in `apps/admin/ui/app` when the SPA changed.
6. **Never skip the hooks.** Do not use `git commit --no-verify` or `-n`. Fix the finding instead.
