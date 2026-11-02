#!/usr/bin/env bash
# Checks the usage event contract (docs/contracts/usage-event, ADR-0010).
#
# Always:
#   1. every schema compiles and its examples pass it;
#   2. every version in participants.json has a schema with examples;
#   3. every version a participant produces on a channel is consumed by every
#      consumer of that channel (expand before switch: reading ahead is fine);
#   4. the gateway's USAGE_URL calls an HTTP version the gateway declares.
# Against BASE (a git ref, e.g. origin/master), on pull requests:
#   5. openapi.yaml has no breaking change (oasdiff);
#   6. no versioned schema that exists in BASE was changed or deleted;
#   7. a version nobody produces any more gets a "stopped" date in the same PR;
#   8. a version leaves a consumer only after it stopped in an earlier PR and
#      the channel's retention has passed since then (contract after switch).
#
# Usage: usage-event.sh [BASE]. Needs jv, oasdiff and jq on PATH.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
dir=docs/contracts/usage-event
manifest=$dir/participants.json
fail=0
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

failed() {
  echo "FAIL $*"
  fail=1
}

# 1. Schemas compile; examples pass. jq keeps number literals as written, so
# 2^53 + 1 reaches jv unchanged.
for s in "$dir"/schemas/*.json; do
  jv "$s" >/dev/null # set -e stops on a schema that does not compile
  n=$(jq '.examples // [] | length' "$s")
  for ((i = 0; i < n; i++)); do
    jq ".examples[$i]" "$s" >"$tmp/example.json"
    if jv -q --assert-format "$s" "$tmp/example.json" >/dev/null 2>&1; then
      echo "ok   $s example $i"
    else
      failed "$s example $i does not pass its own schema:"
      jv --assert-format "$s" "$tmp/example.json" || true
    fi
  done
done

# versions MANIFEST CHANNEL ROLE: every version any participant has in ROLE.
versions() {
  jq -r --arg c "$2" --arg r "$3" '.[$c].participants[][$r] // [] | .[]' "$1" | sort -u
}

for ch in $(jq -r 'keys[]' "$manifest"); do
  schema=$(jq -r --arg c "$ch" '.[$c].schema' "$manifest")

  # 2. Every declared version has a schema with examples.
  for v in $( (versions "$manifest" "$ch" produces; versions "$manifest" "$ch" consumes) | sort -u); do
    f=$dir/schemas/$schema.$v.json
    if [[ ! -f $f ]]; then
      failed "$ch $v is declared in participants.json but $f does not exist"
    elif [[ $(jq '.examples // [] | length' "$f") == 0 ]]; then
      failed "$f has no examples; the services' tests run against them"
    fi
  done

  # 3. Produce only what every consumer reads.
  for v in $(versions "$manifest" "$ch" produces); do
    for consumer in $(jq -r --arg c "$ch" '.[$c].participants | to_entries[] | select(.value.consumes) | .key' "$manifest"); do
      if ! jq -e --arg c "$ch" --arg p "$consumer" --arg v "$v" \
        '.[$c].participants[$p].consumes | index($v)' "$manifest" >/dev/null; then
        failed "$ch $v is produced, but $consumer does not consume it: add it to $consumer first (expand), then switch the producer"
      fi
    done
  done
done

# 4. The gateway's configured endpoint is a version it declares.
url_version=$(sed -n 's#.*usage-writeapi\.usage\.svc:8080/\(v[0-9]*\)/events.*#\1#p' prototype/k8s/apps/gateway/api-gateway.yaml)
if ! jq -e --arg v "$url_version" '.http.participants.gateway.produces | index($v)' "$manifest" >/dev/null; then
  failed "the gateway's USAGE_URL calls ${url_version:-no version}, which gateway does not produce in participants.json"
fi

base=${1:-}
if [[ -z $base ]]; then
  exit $fail
fi

# 5. No breaking change of the HTTP contract.
if git cat-file -e "$base:$dir/openapi.yaml" 2>/dev/null; then
  git archive "$base" "$dir" | tar -x -C "$tmp"
  if ! oasdiff breaking --fail-on ERR "$tmp/$dir/openapi.yaml" "$dir/openapi.yaml"; then
    failed "openapi.yaml has breaking changes against $base: serve a new /vN next to the old one"
  fi
else
  echo "skip oasdiff: $dir/openapi.yaml does not exist in $base"
fi

# 6. Versioned schemas are immutable once merged.
changed=$(git diff --name-only --diff-filter=MDR "$base"...HEAD -- "$dir/schemas")
if [[ -n $changed ]]; then
  failed "versioned schemas changed or deleted (add a new version instead):"
  sed 's/^/  /' <<<"$changed"
fi

if ! git cat-file -e "$base:$manifest" 2>/dev/null; then
  echo "skip version rules: $manifest does not exist in $base"
  exit $fail
fi
git show "$base:$manifest" >"$tmp/base.json"
today=$(date -u +%F)

for ch in $(jq -r 'keys[]' "$manifest"); do
  retention=$(jq -r --arg c "$ch" '.[$c].retention_days' "$manifest")
  produced_now=$(versions "$manifest" "$ch" produces)
  produced_before=$(versions "$tmp/base.json" "$ch" produces)

  # 7. A version that stopped being produced gets its date.
  for v in $(comm -23 <(echo "$produced_before") <(echo "$produced_now")); do
    if ! jq -e --arg c "$ch" --arg v "$v" '.[$c].stopped[$v] | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}$")' "$manifest" >/dev/null 2>&1; then
      failed "$ch $v is no longer produced: set .$ch.stopped.$v to the merge date (YYYY-MM-DD)"
    fi
  done

  # 8. A consumer drops a version only after it stopped and retention passed.
  for p in $(jq -r --arg c "$ch" '.[$c].participants | keys[]' "$tmp/base.json"); do
    before=$(jq -r --arg c "$ch" --arg p "$p" '.[$c].participants[$p].consumes // [] | .[]' "$tmp/base.json" | sort -u)
    now=$(jq -r --arg c "$ch" --arg p "$p" '.[$c].participants[$p].consumes // [] | .[]' "$manifest" | sort -u)
    for v in $(comm -23 <(echo "$before") <(echo "$now")); do
      stopped=$(jq -r --arg c "$ch" --arg v "$v" '.[$c].stopped[$v] // empty' "$tmp/base.json")
      if grep -qx "$v" <<<"$produced_before"; then
        failed "$p drops $ch $v, which was still produced before this PR: stop the producer in an earlier PR"
      elif [[ -z $stopped ]]; then
        failed "$p drops $ch $v, which has no stopped date in $base"
      else
        drained=$(date -u -d "$stopped + $retention days" +%F 2>/dev/null || date -u -j -v+"${retention}"d -f %F "$stopped" +%F)
        if [[ $today < $drained ]]; then
          failed "$p drops $ch $v before $drained: records of it may still be in flight (stopped $stopped + $retention days)"
        else
          echo "ok   $p drops $ch $v (stopped $stopped, drained $drained)"
        fi
      fi
    done
  done
done

exit $fail
