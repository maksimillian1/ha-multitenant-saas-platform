#!/usr/bin/env bash
# Fails if a binary of apps/usage links a package it must not (README.md).
# depguard checks direct imports; this checks the whole linked graph, so a
# chain through another package is caught too. Test imports are not linked.
set -euo pipefail
cd "$(dirname "$0")/.."
export CGO_ENABLED=0 GOOS=linux # the environment of the image build
mod=$(go list -m)
mod=${mod//./\\.}
fail=0

# check CMD REGEX: REGEX matches a whole package path or its prefix up to a "/".
check() {
  local cmd=$1 forbidden="^($2)(/|$)" deps
  # Captured first: if go list fails, set -e stops the script instead of an
  # empty list passing the check.
  deps=$(go list -deps "./cmd/$cmd")
  if grep -Eq "$forbidden" <<<"$deps"; then
    # Only the edges where an allowed package pulls in a forbidden one.
    echo "FAIL cmd/$cmd links forbidden packages through:"
    go list -deps -f '{{.ImportPath}}{{range .Imports}} {{.}}{{end}}' "./cmd/$cmd" |
      awk -v re="$forbidden" '$1 !~ re {for (i = 2; i <= NF; i++) if ($i ~ re) print "  " $1 " -> " $i}'
    fail=1
  else
    echo "ok   cmd/$cmd"
  fi
}

check writeapi "github\.com/jackc/pgx|$mod/internal/(store|writer|migrations|testpg)"
check writer "$mod/internal/(writeapi|migrations|testpg)"
check migrate "github\.com/twmb/franz-go|$mod/internal/(kafka|writeapi|writer|store|testpg)"

exit $fail
