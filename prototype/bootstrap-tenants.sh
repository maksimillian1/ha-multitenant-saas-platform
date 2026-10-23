#!/bin/bash
# Creates the two test tenants of the walking skeleton (HAMSP-5): one Zitadel
# Organization per tenant with one machine user that gets tokens through
# client_credentials. Writes their credentials to prototype/.tenants.json
# (git-ignored) for the e2e tests. Safe to run again: existing organizations and
# users are reused, client secrets are regenerated.
#
# Tenant lifecycle as a feature is HAMSP-6; this script only seeds test data.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY="${GATEWAY:-127.0.0.1:30080}"
ZITADEL_HOST="zitadel.localtest.me:30080"
ISSUER="http://${ZITADEL_HOST}"
OUT="${SCRIPT_DIR}/.tenants.json"
TENANTS=(tenant-a tenant-b)

# Access tokens live 60 s, so the e2e tests can prove that an expired token is
# rejected (HAMSP-5 AC-5) without waiting hours.
ACCESS_TOKEN_LIFETIME="60s"

# ── helpers ────────────────────────────────────────────────────────────────────

# zitadel METHOD PATH [JSON] — calls the Zitadel API through the API Gateway as
# the IAM admin. Set ORG to act inside an organization.
zitadel() {
  local args=(-sS --fail-with-body --connect-to "${ZITADEL_HOST}:${GATEWAY}"
    -X "$1" "${ISSUER}$2"
    -H "Authorization: Bearer ${PAT}" -H "Content-Type: application/json")
  [[ -n "${ORG:-}" ]] && args+=(-H "x-zitadel-orgid: ${ORG}")
  [[ $# -ge 3 ]] && args+=(-d "$3")
  curl "${args[@]}"
}

wait_for() {
  local what="$1" max="$2"; shift 2
  local attempts=0
  until "$@" &>/dev/null; do
    attempts=$((attempts + 1))
    if [[ ${attempts} -ge ${max} ]]; then
      echo "ERROR: ${what} not ready after $((max * 5))s."
      exit 1
    fi
    echo "  waiting for ${what} (${attempts}/${max})..."
    sleep 5
  done
}

zitadel_up() {
  curl -sf --connect-to "${ZITADEL_HOST}:${GATEWAY}" "${ISSUER}/.well-known/openid-configuration"
}

# ── 1. wait for Zitadel and its admin token ────────────────────────────────────

wait_for "IAM admin PAT (zitadel setup job)" 120 \
  kubectl -n zitadel get secret iam-admin-pat
PAT=$(kubectl -n zitadel get secret iam-admin-pat -o jsonpath='{.data.pat}' | base64 -d)
wait_for "Zitadel behind the API Gateway" 60 zitadel_up

# ── 2. short token lifetime for the tests ──────────────────────────────────────

echo "Setting access token lifetime to ${ACCESS_TOKEN_LIFETIME}..."
zitadel PUT /admin/v1/settings/oidc "$(jq -nc --arg l "${ACCESS_TOKEN_LIFETIME}" \
  '{accessTokenLifetime: $l, idTokenLifetime: $l,
    refreshTokenIdleExpiration: "720h", refreshTokenExpiration: "2160h"}')" >/dev/null \
  || echo "  (unchanged)"

# ── 3. one organization and one machine user per tenant ────────────────────────

tenants_json="[]"
for name in "${TENANTS[@]}"; do
  ORG=""
  org_id=$(zitadel POST /admin/v1/orgs/_search "$(jq -nc --arg n "${name}" \
    '{queries: [{nameQuery: {name: $n, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" \
    | jq -r '.result[0].id // empty')
  if [[ -z "${org_id}" ]]; then
    org_id=$(zitadel POST /management/v1/orgs "$(jq -nc --arg n "${name}" '{name: $n}')" | jq -r '.id')
    echo "Created organization ${name} (${org_id})"
  else
    echo "Reusing organization ${name} (${org_id})"
  fi

  ORG="${org_id}"
  username="${name}-client"
  user_id=$(zitadel POST /management/v1/users/_search "$(jq -nc --arg u "${username}" \
    '{queries: [{userNameQuery: {userName: $u, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" \
    | jq -r '.result[0].id // empty')
  if [[ -z "${user_id}" ]]; then
    # JWT access tokens, so the Gateway can validate them locally (ADR-0003).
    user_id=$(zitadel POST /management/v1/users/machine "$(jq -nc --arg u "${username}" --arg n "${name}" \
      '{userName: $u, name: ($n + " API client"), accessTokenType: "ACCESS_TOKEN_TYPE_JWT"}')" \
      | jq -r '.userId')
    echo "  created machine user ${username} (${user_id})"
  fi
  secret=$(zitadel PUT "/management/v1/users/${user_id}/secret" '{}')

  tenants_json=$(jq -c --arg name "${name}" --arg org "${org_id}" \
    --arg cid "$(jq -r '.clientId' <<<"${secret}")" --arg cs "$(jq -r '.clientSecret' <<<"${secret}")" \
    '. + [{name: $name, org_id: $org, client_id: $cid, client_secret: $cs}]' <<<"${tenants_json}")
done
ORG=""

jq -n --arg issuer "${ISSUER}" --arg gw "${GATEWAY}" --argjson t "${tenants_json}" \
  '{issuer: $issuer, gateway: $gw, tenants: $t}' >"${OUT}"
chmod 600 "${OUT}"
echo ""
echo "Wrote ${OUT}. Run the e2e tests:"
echo "  go test -C ${SCRIPT_DIR}/e2e -count=1 ./..."
