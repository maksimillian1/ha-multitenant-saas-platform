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

The rules come from `docs/`: start at `docs/index.md`, read `docs/architecture/requirements.md` (FR, QS, C), and treat Accepted ADRs in `docs/adr/` as overriding everything else. The spec and the plan live in the Linear issue, not in repo files.

1. `feature-clarify HAMSP-N`: asks up to 5 questions and writes acceptance criteria (AC-n) into the issue.
2. `feature-plan HAMSP-N`: checks the approach against the docs and writes steps and a test map into the issue.
3. Implement the steps.
4. `feature-verify HAMSP-N`: runs the tests from the test map. The feature is ready only when every AC and touched QS has a test that ran and passed.

The skills are in `.agents/skills/`; `.claude/skills` links to the same folder.
