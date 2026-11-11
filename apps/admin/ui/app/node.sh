#!/usr/bin/env bash
# node.sh CMD [ARGS...] — runs CMD with the Node version of .nvmrc, whatever
# Node comes first in PATH (lefthook runs with the caller's PATH). The Node in
# PATH is used when it matches (CI: actions/setup-node); otherwise nvm
# switches to the version, if nvm is installed.
set -euo pipefail
cd "$(dirname "$0")"
want=v$(cat .nvmrc)
if [ "$(node -v 2>/dev/null || true)" != "$want" ] && [ -s "${NVM_DIR:-$HOME/.nvm}/nvm.sh" ]; then
  # shellcheck disable=SC1091
  . "${NVM_DIR:-$HOME/.nvm}/nvm.sh" --no-use
  nvm use --silent "$want" >/dev/null || { echo "node.sh: Node $want is not installed: nvm install $want" >&2; exit 1; }
fi
have=$(node -v 2>/dev/null || true)
if [ "$have" != "$want" ]; then
  echo "node.sh: Node $have, want $want (.nvmrc)" >&2
  exit 1
fi
exec "$@"
