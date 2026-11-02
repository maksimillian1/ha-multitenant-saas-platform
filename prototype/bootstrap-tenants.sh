#!/bin/bash
# Creates the two test tenants of the walking skeleton (HAMSP-5): one Zitadel
# Organization per tenant with one machine user that gets tokens through
# client_credentials. Also creates the platform API project, whose ID is the
# audience the API Gateway requires (HAMSP-22), and hands that ID to the
# Gateway. For the super admin app (apps/admin) it creates the platform-admin
# project with the super-admin and viewer roles, an OIDC web app and three
# human test users, super-admin, viewer and regular-user (no role), and writes
# the app's client to the Secret admin/admin-oidc. Writes the credentials to
# prototype/.tenants.json (git-ignored) for the e2e tests and for signing in.
# Safe to run again: existing organizations, users and projects are reused;
# client secrets and the admin test users' passwords are regenerated.
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
# The super admin app (apps/admin) and its test users.
ADMIN_PROJECT="platform-admin"
ADMIN_APP="admin"
ADMIN_ROLE="super-admin"
# Lets a user sign in to the admin app without access to its API, to see the
# access denied page.
VIEWER_ROLE="viewer"
ADMIN_URL="http://admin.localtest.me:30080"
ADMIN_EMAIL_DOMAIN="admin.localtest.me"

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

# ── 5. the super admin app (apps/admin): browser sign-in, project role ─────────

# The instance requires Login V2, which this cluster does not deploy (the
# chart's login is disabled). Zitadel's built-in login serves browser sign-in
# once the requirement is off.
echo "Using Zitadel's built-in login for browser sign-in..."
zitadel PUT /v2/features/instance '{"loginV2": {"required": false}}' >/dev/null

# The admin API requires the project role ${ADMIN_ROLE}. With role assertion on
# the project and on the app, Zitadel puts the user's roles into the ID token.
# With the role check, Zitadel signs in only users with a role in the project,
# so tenants' users never get a session or the SPA.
admin_project=$(jq -nc --arg n "${ADMIN_PROJECT}" '{name: $n, projectRoleAssertion: true, projectRoleCheck: true}')
admin_project_id=$(zitadel POST /management/v1/projects/_search "$(jq -nc --arg n "${ADMIN_PROJECT}" \
  '{queries: [{nameQuery: {name: $n, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" \
  | jq -r '.result[0].id // empty')
if [[ -z "${admin_project_id}" ]]; then
  admin_project_id=$(zitadel POST /management/v1/projects "${admin_project}" | jq -r '.id')
  echo "Created project ${ADMIN_PROJECT} (${admin_project_id})"
else
  echo "Reusing project ${ADMIN_PROJECT} (${admin_project_id})"
  # A project from an earlier run may lack the role check; "No changes" is fine.
  if ! out=$(zitadel PUT "/management/v1/projects/${admin_project_id}" "${admin_project}"); then
    grep -q "No changes" <<<"${out}" || { echo "ERROR: ${out}"; exit 1; }
  fi
fi

for role in "${ADMIN_ROLE}" "${VIEWER_ROLE}"; do
  role_count=$(zitadel POST "/management/v1/projects/${admin_project_id}/roles/_search" "$(jq -nc --arg r "${role}" \
    '{queries: [{keyQuery: {key: $r, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" | jq '.result // [] | length')
  if [[ "${role_count}" == "0" ]]; then
    zitadel POST "/management/v1/projects/${admin_project_id}/roles" "$(jq -nc --arg r "${role}" \
      '{roleKey: $r, displayName: $r}')" >/dev/null
    echo "  created role ${role}"
  fi
done

# A confidential web app; devMode allows the plain-HTTP redirect URI.
app=$(zitadel POST "/management/v1/projects/${admin_project_id}/apps/_search" "$(jq -nc --arg n "${ADMIN_APP}" \
  '{queries: [{nameQuery: {name: $n, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" | jq -c '.result[0] // empty')
if [[ -z "${app}" ]]; then
  created=$(zitadel POST "/management/v1/projects/${admin_project_id}/apps/oidc" "$(jq -nc \
    --arg n "${ADMIN_APP}" --arg url "${ADMIN_URL}" \
    '{name: $n, redirectUris: [$url + "/auth/callback"], postLogoutRedirectUris: [$url + "/"],
      responseTypes: ["OIDC_RESPONSE_TYPE_CODE"], grantTypes: ["OIDC_GRANT_TYPE_AUTHORIZATION_CODE"],
      appType: "OIDC_APP_TYPE_WEB", authMethodType: "OIDC_AUTH_METHOD_TYPE_BASIC", devMode: true,
      idTokenRoleAssertion: true, idTokenUserinfoAssertion: true}')")
  admin_client_id=$(jq -r '.clientId' <<<"${created}")
  admin_client_secret=$(jq -r '.clientSecret' <<<"${created}")
  echo "  created OIDC app ${ADMIN_APP}"
else
  admin_client_id=$(jq -r '.oidcConfig.clientId' <<<"${app}")
  admin_client_secret=$(zitadel POST "/management/v1/projects/${admin_project_id}/apps/$(jq -r '.id' <<<"${app}")/oidc_config/_generate_client_secret" \
    '{}' | jq -r '.clientSecret')
  echo "  reusing OIDC app ${ADMIN_APP}, client secret regenerated"
fi

# human_user USERNAME — creates the user in the IAM admin's organization, or
# resets its password, and prints "user_id password".
human_user() {
  local username="$1" password user_id
  # Meets Zitadel's default complexity: upper, lower, digit, symbol.
  password="Pw1!$(openssl rand -hex 12)"
  user_id=$(zitadel POST /v2/users "$(jq -nc --arg u "${username}" \
    '{queries: [{userNameQuery: {userName: $u, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" | jq -r '.result[0].userId // empty')
  if [[ -z "${user_id}" ]]; then
    user_id=$(zitadel POST /v2/users/human "$(jq -nc --arg u "${username}" --arg p "${password}" --arg d "${ADMIN_EMAIL_DOMAIN}" \
      '{username: $u, profile: {givenName: $u, familyName: "(test)"},
        email: {email: ($u + "@" + $d), isVerified: true},
        password: {password: $p, changeRequired: false}}')" | jq -r '.userId')
    echo "  created user ${username}" >&2
  else
    zitadel POST "/v2/users/${user_id}/password" "$(jq -nc --arg p "${password}" \
      '{newPassword: {password: $p, changeRequired: false}}')" >/dev/null
    echo "  reusing user ${username}, password reset" >&2
  fi
  echo "${user_id} ${password}"
}

# grant USER_ID USERNAME ROLE — gives the user the project role once.
grant() {
  local grants
  grants=$(zitadel POST /management/v1/users/grants/_search "$(jq -nc --arg u "$1" --arg p "${admin_project_id}" \
    '{queries: [{userIdQuery: {userId: $u}}, {projectIdQuery: {projectId: $p}}]}')" | jq '.result // [] | length')
  if [[ "${grants}" == "0" ]]; then
    zitadel POST "/management/v1/users/$1/grants" "$(jq -nc --arg p "${admin_project_id}" --arg r "$3" \
      '{projectId: $p, roleKeys: [$r]}')" >/dev/null
    echo "  granted $3 to $2"
  fi
}

read -r super_admin_id super_admin_password < <(human_user super-admin)
read -r viewer_id viewer_password < <(human_user viewer)
read -r regular_user_id regular_user_password < <(human_user regular-user)
if [[ -z "${super_admin_id}" || -z "${viewer_id}" || -z "${regular_user_id}" ]]; then
  echo "ERROR: creating the admin test users failed."
  exit 1
fi
grant "${super_admin_id}" super-admin "${ADMIN_ROLE}"
grant "${viewer_id}" viewer "${VIEWER_ROLE}"

# The admin app reads its client and session key from this Secret (k8s/apps/admin).
# A new session key signs everyone out, which is fine for test users.
kubectl create namespace admin --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n admin create secret generic admin-oidc \
  --from-literal=CLIENT_ID="${admin_client_id}" --from-literal=CLIENT_SECRET="${admin_client_secret}" \
  --from-literal=SESSION_KEY="$(openssl rand -hex 32)" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
if kubectl -n admin get deployment admin &>/dev/null; then
  kubectl -n admin rollout restart deployment/admin >/dev/null
fi

admin_json=$(jq -nc --arg url "${ADMIN_URL}" --arg role "${ADMIN_ROLE}" --arg vrole "${VIEWER_ROLE}" \
  --arg sp "${super_admin_password}" --arg vp "${viewer_password}" --arg rp "${regular_user_password}" \
  '{url: $url, role: $role, users: [{username: "super-admin", password: $sp, roles: [$role]},
                                    {username: "viewer", password: $vp, roles: [$vrole]},
                                    {username: "regular-user", password: $rp, roles: []}]}')

jq -n --arg issuer "${ISSUER}" --arg gw "${GATEWAY}" --arg aud "${project_id}" --argjson t "${tenants_json}" \
  --argjson admin "${admin_json}" \
  '{issuer: $issuer, gateway: $gw, audience: $aud, tenants: $t, admin: $admin}' >"${OUT}"
chmod 600 "${OUT}"
echo ""
echo "Wrote ${OUT}. Super admin page: ${ADMIN_URL} (users: jq .admin.users ${OUT})"
echo "Run the e2e tests:"
echo "  go test -C ${SCRIPT_DIR}/e2e -count=1 ./..."
