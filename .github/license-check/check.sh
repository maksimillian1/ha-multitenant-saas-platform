#!/bin/bash
# Fails if a Go module depends, directly or transitively, on a package whose
# license is forbidden (AGPL and the like), restricted (GPL, LGPL) or unknown
# (C-03, ADR-0003). Usage: check.sh <module dir>...
#
# Needs go-licenses v2 on PATH:
#   go install github.com/google/go-licenses/v2@v2.0.1
set -euo pipefail

[[ $# -gt 0 ]] || { echo "usage: $0 <module dir>..." >&2; exit 2; }

status=0
for dir in "$@"; do
  # The module's own packages have no LICENSE next to them (it is at the repo
  # root), so they would count as unknown.
  self=$(go -C "${dir}" list -m)
  echo "== ${dir} (${self})"
  if ! (cd "${dir}" && go-licenses check ./... --ignore "${self}" \
      --disallowed_types=forbidden,restricted,unknown); then
    status=1
  fi
done
exit "${status}"
