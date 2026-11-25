#!/usr/bin/env bash
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
fixtures="$here/testdata"

pass=0
fail=0
ok() { echo "ok   $1"; pass=$((pass + 1)); }
bad() {
  echo "FAIL $1"
  [ -n "${2:-}" ] && echo "$2" | tail -20 | sed 's/^/     /'
  fail=$((fail + 1))
}
expect_refused() {
  if [ "$4" -ne 0 ] && grep -Eq "$2" <<<"$3"; then ok "$1"; else bad "$1 (rc=$4)" "$3"; fi
}
expect_passed() {
  if [ "$3" -eq 0 ]; then ok "$1"; else bad "$1 (rc=$3)" "$2"; fi
}

# Verifies: guardrails AC-10
case_govulncheck() {
  local out rc
  out=$(cd "$fixtures/vulnerable-go" && govulncheck ./... 2>&1); rc=$?
  expect_refused "AC-10 govulncheck fails on a reachable vulnerability and names it" "GO-2022-1059" "$out" $rc
}

# Verifies: guardrails AC-11
case_npm_audit() {
  local out rc
  out=$(cd "$fixtures/npm-prod" && npm audit --omit=dev --audit-level=high 2>&1); rc=$?
  expect_refused "AC-11 npm audit fails on a production dependency with a critical advisory" "Severity: critical" "$out" $rc
  out=$(cd "$fixtures/npm-dev" && npm audit --omit=dev --audit-level=high 2>&1); rc=$?
  expect_passed "AC-11 npm audit passes when only a dev dependency has the advisory" "$out" $rc
}

[ $# -gt 0 ] || set -- $(declare -F | awk '$3 ~ /^case_/ {print $3}')
for c in "$@"; do "$c"; done

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
