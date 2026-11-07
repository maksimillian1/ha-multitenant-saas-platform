#!/usr/bin/env bash
# Claude Code PreToolUse hook for Bash `git commit` (registered in .claude/settings.json).
#
# For Claude only; people use lefthook as is (lefthook.yml). Before Claude commits:
#   1. refuses --no-verify / -n: the checks are not optional;
#   2. formats the staged Go files (golangci-lint fmt) and stages the result,
#      but only files with no unstaged changes, so nothing unstaged slips in;
#   3. runs the lefthook pre-commit checks and blocks the commit if one fails.
# Exit 2 blocks the tool call; stderr goes back to Claude.
set -uo pipefail

payload=$(cat)
cmd=$(jq -r '.tool_input.command // ""' <<<"$payload")
cwd=$(jq -r '.cwd // empty' <<<"$payload")
[ -n "$cwd" ] && cd "$cwd" 2>/dev/null

# Only `git commit` (also after `cd ... &&` or with git -C).
if ! grep -Eq '(^|[;&|[:space:]])git([[:space:]]+-C[[:space:]]+[^[:space:]]+)?[[:space:]]+commit([[:space:]]|$)' <<<"$cmd"; then
  exit 0
fi

if grep -Eq '(^|[[:space:]])(--no-verify|-n)([[:space:]]|$)' <<<"$cmd"; then
  echo "Blocked: git commit --no-verify skips the pre-commit checks. Fix the findings and commit without it." >&2
  exit 2
fi

root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
cd "$root" || exit 0
[ -f lefthook.yml ] || exit 0

# 2. Format staged Go files that are fully staged.
fixed=()
while IFS= read -r f; do
  [ -n "$f" ] || continue
  if git diff --quiet -- "$f"; then
    fixed+=("$f")
  fi
done < <(git diff --cached --name-only --diff-filter=ACM -- 'apps/*.go' 'apps/**/*.go')

if [ ${#fixed[@]} -gt 0 ]; then
  if ! out=$(golangci-lint fmt --config .golangci.yml "${fixed[@]}" 2>&1); then
    echo "golangci-lint fmt failed:" >&2
    echo "$out" >&2
    exit 2
  fi
  git add -- "${fixed[@]}"
fi

# 3. The same checks as the git hook; block with their output.
if ! out=$(NO_COLOR=1 lefthook run pre-commit --no-tty 2>&1); then
  echo "Blocked: lefthook pre-commit failed. Fix the findings, stage the fixes and commit again." >&2
  echo "$out" | grep -vE '^\s*$|\(skip\)' | tail -60 >&2
  exit 2
fi
exit 0
