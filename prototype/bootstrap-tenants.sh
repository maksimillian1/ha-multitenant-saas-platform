#!/bin/bash
# Creates the two test tenants of the walking skeleton (HAMSP-5): one Zitadel
# Organization per tenant with one machine user that gets tokens through
# client_credentials. Also creates the platform API project, whose ID is the
# audience the API Gateway requires (HAMSP-22), and hands that ID to the
# Gateway. Writes the credentials to prototype/.tenants.json (git-ignored) for
# the e2e tests. Safe to run again: existing organizations, users and the
# project are reused, client secrets are regenerated.
#
# Tenant lifecycle as a feature is HAMSP-6; this script only seeds test data.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY="${GATEWAY:-127.0.0.1:30080}"
ZITADEL_HOST="zitadel.localtest.me:30080"
ISSUER="http://${ZITADEL_HOST}"
OUT="${SCRIPT_DIR}/.tenants.json"
TENANTS=(tenant-a tenant-b)
PROJECT="platform-api"

# Access tokens live 60 s, so the e2e tests can prove that an expired token is
# rejected (HAMSP-5 AC-5) without waiting hours.
ACCESS_TOKEN_LIFETIME="60s"  # protobuf Duration: seconds only

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
# On a rerun Zitadel answers 400 "No changes"; any other error stops the script.
if ! out=$(zitadel PUT /admin/v1/settings/oidc "$(jq -nc --arg l "${ACCESS_TOKEN_LIFETIME}" \
  '{accessTokenLifetime: $l, idTokenLifetime: $l,
    refreshTokenIdleExpiration: "2592000s", refreshTokenExpiration: "7776000s"}')"); then
  grep -q "No changes" <<<"${out}" || { echo "ERROR: ${out}"; exit 1; }
fi

# ── 3. the platform API project: its ID is the token audience ──────────────────

# Created in the IAM admin's organization. Tenant organizations get no project
# grants yet, so any user of the instance can still ask for this audience;
# restricting it is part of the tenant lifecycle (HAMSP-6).
project_id=$(zitadel POST /management/v1/projects/_search "$(jq -nc --arg n "${PROJECT}" \
  '{queries: [{nameQuery: {name: $n, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" \
  | jq -r '.result[0].id // empty')
if [[ -z "${project_id}" ]]; then
  project_id=$(zitadel POST /management/v1/projects "$(jq -nc --arg n "${PROJECT}" '{name: $n}')" | jq -r '.id')
  echo "Created project ${PROJECT} (${project_id})"
else
  echo "Reusing project ${PROJECT} (${project_id})"
fi

# The API Gateway reads the audience from this ConfigMap (k8s/apps/gateway).
wait_for "API Gateway Deployment (ArgoCD)" 60 kubectl -n gateway get deployment api-gateway
current=$(kubectl -n gateway get configmap api-gateway-audience -o jsonpath='{.data.audience}' 2>/dev/null || true)
if [[ "${current}" != "${project_id}" ]]; then
  echo "Setting the API Gateway audience to ${project_id}..."
  kubectl -n gateway create configmap api-gateway-audience --from-literal=audience="${project_id}" \
    --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n gateway rollout restart deployment/api-gateway
fi
kubectl -n gateway rollout status deployment/api-gateway --timeout=3m

# ── 4. one organization and one machine user per tenant ────────────────────────

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

jq -n --arg issuer "${ISSUER}" --arg gw "${GATEWAY}" --arg aud "${project_id}" --argjson t "${tenants_json}" \
  '{issuer: $issuer, gateway: $gw, audience: $aud, tenants: $t}' >"${OUT}"
chmod 600 "${OUT}"
echo ""
echo "Wrote ${OUT}. Run the e2e tests:"
echo "  go test -C ${SCRIPT_DIR}/e2e -count=1 ./..."
