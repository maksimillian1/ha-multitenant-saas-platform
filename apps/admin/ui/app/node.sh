#!/usr/bin/env bash
# node.sh CMD [ARGS...] — runs CMD with the Node version of .nvmrc, whatever
# Node comes first in PATH (lefthook runs with the caller's PATH). Uses nvm
# when it is installed; otherwise the Node in PATH must already match.
set -euo pipefail
cd "$(dirname "$0")"
want=$(cat .nvmrc)
if [ -s "${NVM_DIR:-$HOME/.nvm}/nvm.sh" ]; then
  # shellcheck disable=SC1091
  . "${NVM_DIR:-$HOME/.nvm}/nvm.sh" --no-use
  nvm use --silent "$want" >/dev/null || { echo "node.sh: Node $want is not installed: nvm install $want" >&2; exit 1; }
fi
have=$(node -v 2>/dev/null || true)
if [ "$have" != "v$want" ]; then
  echo "node.sh: Node $have, want v$want (.nvmrc)" >&2
  exit 1
fi
exec "$@"
