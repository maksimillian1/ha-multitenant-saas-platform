#!/usr/bin/env bash
# Tests of the commit guardrails (HAMSP-40, AC-1..AC-7 and AC-9).
#
# Each case stages a bad change in a throwaway git worktree of HEAD and checks
# that the right hook or CI command refuses it. The guardrail files are copied
# from the working tree, so uncommitted changes to them are tested too.
#
# Needs on PATH: git, jq, lefthook, golangci-lint, gitleaks, go, node/npx; and
# apps/admin/ui/app/node_modules (npm ci). Run from anywhere in the repo:
#   tools/hooktest/test.sh
set -uo pipefail

repo=$(git rev-parse --show-toplevel)
tmp=$(mktemp -d)
wt="$tmp/wt"
git -C "$repo" worktree add --detach -q "$wt" HEAD
cleanup() {
  git -C "$repo" worktree remove --force "$wt" >/dev/null 2>&1
  rm -rf "$tmp"
}
trap cleanup EXIT

g() { git -C "$wt" -c user.name=hooktest -c user.email=hooktest@example.invalid -c core.hooksPath=/dev/null "$@"; }

for f in lefthook.yml .golangci.yml .claude/hooks/pre-commit.sh; do
  mkdir -p "$wt/$(dirname "$f")"
  cp "$repo/$f" "$wt/$f"
done
spa=apps/admin/ui/app
if [ ! -d "$repo/$spa/node_modules" ]; then
  echo "missing $spa/node_modules: run npm ci there" >&2
  exit 1
fi
ln -s "$repo/$spa/node_modules" "$wt/$spa/node_modules"
g add -A -- lefthook.yml .golangci.yml .claude/hooks/pre-commit.sh
g commit -q --allow-empty -m "hooktest base"
base=$(g rev-parse HEAD)

pass=0
fail=0
ok() { echo "ok   $1"; pass=$((pass + 1)); }
bad() {
  echo "FAIL $1"
  [ -n "${2:-}" ] && echo "$2" | tail -20 | sed 's/^/     /'
  fail=$((fail + 1))
}
reset() {
  g reset -q --hard "$base"
  g clean -qfd
}
hook() { (cd "$wt" && NO_COLOR=1 lefthook run pre-commit --no-tty 2>&1); }
lint() { (cd "$wt/apps/$1" && golangci-lint run --allow-parallel-runners "${@:2}" ./... 2>&1); }
put() { mkdir -p "$wt/$(dirname "$1")" && printf '%b' "$2" > "$wt/$1"; }

# expect_refused NAME PATTERN OUTPUT RC: the command failed and said PATTERN.
expect_refused() {
  if [ "$4" -ne 0 ] && grep -Eq "$2" <<<"$3"; then ok "$1"; else bad "$1 (rc=$4)" "$3"; fi
}

# Verifies: HAMSP-40 AC-1
token="ghp_$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 36)"
put apps/gateway/core/zz_secret.go "package core\n\nconst hookTestToken = \"$token\"\n"
g add apps/gateway/core/zz_secret.go
out=$(hook); rc=$?
expect_refused "AC-1 pre-commit refuses a staged secret" "leaks found: [1-9]" "$out" $rc
g commit -q -m "secret"
out=$(cd "$wt" && gitleaks git --redact --no-banner --log-opts="$base..HEAD" . 2>&1); rc=$?
expect_refused "AC-1 CI gitleaks refuses a secret in the PR's commits" "leaks found: [1-9]" "$out" $rc
reset

# Verifies: HAMSP-40 AC-2
put apps/gateway/core/zz_nolint.go 'package core\n\nimport "encoding/json"\n\nfunc hookTestNoReason(b []byte, v any) {\n\tjson.Unmarshal(b, v) //nolint:errcheck\n}\n'
g add apps/gateway/core/zz_nolint.go
out=$(hook); rc=$?
expect_refused "AC-2 pre-commit refuses //nolint without a reason" "nolintlint" "$out" $rc
out=$(lint gateway --config ../../.golangci.yml); rc=$?
expect_refused "AC-2 CI lint refuses //nolint without a reason" "nolintlint" "$out" $rc
reset

# Verifies: HAMSP-40 AC-3
put apps/gateway/core/zz_old.go 'package core\n\nimport "encoding/json"\n\nfunc hookTestOldFinding(b []byte, v any) {\n\tjson.Unmarshal(b, v)\n}\n'
g add apps/gateway/core/zz_old.go
g commit -q -m "old finding"
put apps/gateway/core/zz_new.go 'package core\n\nfunc hookTestClean() int { return 1 }\n'
g add apps/gateway/core/zz_new.go
out=$(hook); rc=$?
if [ $rc -eq 0 ]; then ok "AC-3 pre-commit does not block on a finding in unchanged lines"; else bad "AC-3 pre-commit blocked on an old finding" "$out"; fi
out=$(lint gateway --config ../../.golangci.yml); rc=$?
expect_refused "AC-3 CI lint fails on any finding, old ones too" "errcheck" "$out" $rc
reset
put apps/gateway/core/zz_new.go 'package core\n\nimport "encoding/json"\n\nfunc hookTestNewFinding(b []byte, v any) {\n\tjson.Unmarshal(b, v)\n}\n'
g add apps/gateway/core/zz_new.go
out=$(hook); rc=$?
expect_refused "AC-3 pre-commit refuses a new finding" "errcheck" "$out" $rc
reset

# Verifies: HAMSP-40 AC-4
put apps/gateway/core/zz_fmt.go 'package core\n\nfunc   hookTestFmt( ) {}\n'
g add apps/gateway/core/zz_fmt.go
before=$(g rev-parse :apps/gateway/core/zz_fmt.go)
sum=$(shasum "$wt/apps/gateway/core/zz_fmt.go")
out=$(hook); rc=$?
expect_refused "AC-4 go-fmt refuses an unformatted file and shows the diff" "\+func hookTestFmt\(\) \{\}" "$out" $rc
if [ "$before" = "$(g rev-parse :apps/gateway/core/zz_fmt.go)" ] && [ "$sum" = "$(shasum "$wt/apps/gateway/core/zz_fmt.go")" ]; then
  ok "AC-4 go-fmt changes neither the file nor the index"
else
  bad "AC-4 go-fmt changed the file or the index"
fi
reset

# Verifies: HAMSP-40 AC-5
put apps/usage/internal/core/zz_dep.go 'package core\n\nimport "github.com/jackc/pgx/v5"\n\nvar hookTestDep = pgx.Identifier{}\n'
g add apps/usage/internal/core/zz_dep.go
out=$(hook); rc=$?
expect_refused "AC-5 pre-commit refuses a forbidden import in apps/usage" "depguard" "$out" $rc
out=$(lint usage); rc=$?
expect_refused "AC-5 CI boundaries lint refuses a forbidden import" "depguard" "$out" $rc
reset

# Verifies: HAMSP-40 AC-6
put $spa/src/zz_types.ts 'export const hookTestNumber: number = "not a number";\n'
g add $spa/src/zz_types.ts
out=$(hook); rc=$?
expect_refused "AC-6 pre-commit refuses a tsc error" "TS2322|not assignable" "$out" $rc
reset
put $spa/src/zz_hooks.ts 'import { useState } from "react";\n\nexport function useHookTest(on: boolean) {\n  if (on) {\n    useState(0);\n  }\n}\n'
g add $spa/src/zz_hooks.ts
out=$(hook); rc=$?
expect_refused "AC-6 pre-commit refuses an oxlint error" "rules-of-hooks" "$out" $rc
reset

# Verifies: HAMSP-40 AC-7
claude() { jq -nc --arg c "$1" --arg d "$wt" '{tool_name:"Bash",tool_input:{command:$c},cwd:$d}' | "$wt/.claude/hooks/pre-commit.sh" 2>&1; }
out=$(claude "ls -la"); rc=$?
if [ $rc -eq 0 ]; then ok "AC-7 Claude hook ignores other commands"; else bad "AC-7 Claude hook blocked a non-commit command" "$out"; fi
for c in "git commit --no-verify -m x" "git commit -n -m x" "git commit -am x --no-verify"; do
  out=$(claude "$c"); rc=$?
  if [ $rc -eq 2 ]; then ok "AC-7 Claude hook refuses: $c"; else bad "AC-7 Claude hook let through: $c (rc=$rc)" "$out"; fi
done
put apps/gateway/core/zz_fmt.go 'package core\n\nfunc   hookTestFmt( ) {}\n'
g add apps/gateway/core/zz_fmt.go
out=$(claude "git commit -m x"); rc=$?
if [ $rc -eq 0 ] && g show :apps/gateway/core/zz_fmt.go | grep -q '^func hookTestFmt() {}$'; then
  ok "AC-7 Claude hook formats and stages a fully staged file"
else
  bad "AC-7 Claude hook did not format and stage (rc=$rc)" "$out"
fi
reset
put apps/gateway/core/zz_fmt.go 'package core\n\nfunc   hookTestFmt( ) {}\n'
g add apps/gateway/core/zz_fmt.go
printf '\n// unstaged\n' >> "$wt/apps/gateway/core/zz_fmt.go"
before=$(g rev-parse :apps/gateway/core/zz_fmt.go)
out=$(claude "git commit -m x"); rc=$?
if [ $rc -eq 2 ] && [ "$before" = "$(g rev-parse :apps/gateway/core/zz_fmt.go)" ]; then
  ok "AC-7 Claude hook leaves a partly staged file and blocks"
else
  bad "AC-7 Claude hook touched a partly staged file (rc=$rc)" "$out"
fi
reset
put apps/gateway/core/zz_new.go 'package core\n\nimport "encoding/json"\n\nfunc hookTestNewFinding(b []byte, v any) {\n\tjson.Unmarshal(b, v)\n}\n'
g add apps/gateway/core/zz_new.go
out=$(claude "git commit -m x"); rc=$?
if [ $rc -eq 2 ] && grep -q errcheck <<<"$out"; then ok "AC-7 Claude hook blocks when lefthook fails"; else bad "AC-7 Claude hook did not block a lint finding (rc=$rc)" "$out"; fi
reset

# Verifies: HAMSP-40 AC-9
if grep -q '^## Guardrails$' "$repo/AGENTS.md"; then ok "AC-9 AGENTS.md has a Guardrails section"; else bad "AC-9 AGENTS.md has no Guardrails section"; fi
for s in feature-clarify feature-plan feature-verify; do
  if grep -q 'AGENTS.md`, section Guardrails' "$repo/.agents/skills/$s/SKILL.md"; then
    ok "AC-9 $s references the Guardrails"
  else
    bad "AC-9 $s does not reference the Guardrails"
  fi
done

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
