#!/bin/bash
# Creates the two test tenants of the walking skeleton (HAMSP-5): one Zitadel
# Organization per tenant with one machine user that gets tokens through
# client_credentials. Also creates the platform API project, whose ID is the
# audience the API Gateway requires (HAMSP-22), and hands that ID and the
# tenants' organization IDs to the Gateway, which serves only those
# organizations (HAMSP-24). Two more machine users, in the organization
# outsider and in the IAM admin's, are not tenants; the e2e tests use them.
# For the super admin app (apps/admin) it creates the platform-admin
# project with the super-admin and viewer roles, an OIDC web app and three
# human test users, super-admin, viewer and regular-user (no role), and writes
# the app's client to the Secret admin/admin-oidc. It also creates the app's
# service account admin-app with the instance role IAM_ORG_MANAGER, which the
# app uses to manage organizations, users and role assignments (HAMSP-34), and
# a second one, admin-app-instance with IAM_OWNER, which the app uses only for
# the platform (instance) branding (HAMSP-37). Their personal access tokens go
# to the Secret admin/admin-zitadel (ZITADEL_TOKEN, ZITADEL_INSTANCE_TOKEN). Writes
# the credentials to prototype/.tenants.json (git-ignored) for the e2e tests
# and for signing in. Safe to run again: existing organizations, users and
# projects are reused; client secrets, the service account's token and the
# admin test users' passwords are regenerated.
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
# The admin app calls the Zitadel APIs as this machine user. IAM_ORG_MANAGER
# covers the organizations, users, role assignments and projects of every
# organization, without the instance's settings.
ADMIN_SA="admin-app"
ADMIN_SA_ROLE="IAM_ORG_MANAGER"
# The instance's branding needs iam.policy.write, which only IAM_OWNER has.
# The admin app uses this account for the platform branding routes and
# nothing else.
INSTANCE_SA="admin-app-instance"
INSTANCE_SA_ROLE="IAM_OWNER"

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

# Created in the IAM admin's organization. Zitadel gives this audience to a
# machine user of any organization, project grants or not (HAMSP-24), so the
# API Gateway also checks the token's organization against its tenant list
# (section 5).
project_id=$(zitadel POST /management/v1/projects/_search "$(jq -nc --arg n "${PROJECT}" \
  '{queries: [{nameQuery: {name: $n, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" \
  | jq -r '.result[0].id // empty')
if [[ -z "${project_id}" ]]; then
  project_id=$(zitadel POST /management/v1/projects "$(jq -nc --arg n "${PROJECT}" '{name: $n}')" | jq -r '.id')
  echo "Created project ${PROJECT} (${project_id})"
else
  echo "Reusing project ${PROJECT} (${project_id})"
fi

# ── 4. one organization and one machine user per tenant ────────────────────────

# org_named NAME — prints the ID of the organization NAME, created if missing.
org_named() {
  local id
  id=$(ORG="" zitadel POST /admin/v1/orgs/_search "$(jq -nc --arg n "$1" \
    '{queries: [{nameQuery: {name: $n, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" \
    | jq -r '.result[0].id // empty')
  if [[ -z "${id}" ]]; then
    id=$(ORG="" zitadel POST /management/v1/orgs "$(jq -nc --arg n "$1" '{name: $n}')" | jq -r '.id')
    echo "Created organization $1 (${id})" >&2
  else
    echo "Reusing organization $1 (${id})" >&2
  fi
  echo "${id}"
}

# machine_client ORG_ID NAME — prints {name, org_id, client_id, client_secret}
# of the machine user NAME-client in the organization, created if missing,
# with a new client secret.
machine_client() {
  local org_id="$1" name="$2" username="$2-client" user_id secret
  user_id=$(ORG="${org_id}" zitadel POST /management/v1/users/_search "$(jq -nc --arg u "${username}" \
    '{queries: [{userNameQuery: {userName: $u, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" \
    | jq -r '.result[0].id // empty')
  if [[ -z "${user_id}" ]]; then
    # JWT access tokens, so the Gateway can validate them locally (ADR-0003).
    user_id=$(ORG="${org_id}" zitadel POST /management/v1/users/machine "$(jq -nc --arg u "${username}" --arg n "${name}" \
      '{userName: $u, name: ($n + " API client"), accessTokenType: "ACCESS_TOKEN_TYPE_JWT"}')" \
      | jq -r '.userId')
    echo "  created machine user ${username} (${user_id})" >&2
  fi
  secret=$(ORG="${org_id}" zitadel PUT "/management/v1/users/${user_id}/secret" '{}')
  jq -nc --arg name "${name}" --arg org "${org_id}" \
    --arg cid "$(jq -r '.clientId' <<<"${secret}")" --arg cs "$(jq -r '.clientSecret' <<<"${secret}")" \
    '{name: $name, org_id: $org, client_id: $cid, client_secret: $cs}'
}

tenants_json="[]"
for name in "${TENANTS[@]}"; do
  client=$(machine_client "$(org_named "${name}")" "${name}")
  tenants_json=$(jq -c --argjson c "${client}" '. + [$c]' <<<"${tenants_json}")
done

# Machine users of organizations that are not tenants (HAMSP-24): one in an
# organization of its own, one in the IAM admin's. Zitadel gives both a token
# with the platform API audience; the e2e tests prove the Gateway rejects it.
iam_org_id=$(ORG="" zitadel GET /management/v1/orgs/me | jq -r '.org.id')
non_tenants_json=$(jq -nc --argjson a "$(machine_client "$(org_named outsider)" outsider)" \
  --argjson b "$(machine_client "${iam_org_id}" iam-admin-org)" '[$a, $b]')

# ── 5. the API Gateway's audience and tenant list ──────────────────────────────

# The API Gateway reads both from ConfigMaps (k8s/apps/gateway) and accepts a
# token only with this audience and of one of these organizations (HAMSP-22,
# HAMSP-24). It restarts only when one of them changed.
gateway_changed=false
# gateway_config NAME KEY VALUE — writes the ConfigMap gateway/NAME if KEY differs.
gateway_config() {
  local current
  current=$(kubectl -n gateway get configmap "$1" -o jsonpath="{.data.$2}" 2>/dev/null || true)
  if [[ "${current}" != "$3" ]]; then
    echo "Setting the API Gateway $2 to $3..."
    kubectl -n gateway create configmap "$1" --from-literal="$2=$3" --dry-run=client -o yaml | kubectl apply -f -
    gateway_changed=true
  fi
}
wait_for "API Gateway Deployment (ArgoCD)" 60 kubectl -n gateway get deployment api-gateway
gateway_config api-gateway-audience audience "${project_id}"
gateway_config api-gateway-tenants tenants "$(jq -r 'map(.org_id) | join(",")' <<<"${tenants_json}")"
if [[ "${gateway_changed}" == true ]]; then
  kubectl -n gateway rollout restart deployment/api-gateway
else
  echo "API Gateway audience and tenants unchanged"
fi
kubectl -n gateway rollout status deployment/api-gateway --timeout=3m

# ── 6. the super admin app (apps/admin): browser sign-in, project role ─────────

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

# ── 7. the admin app's service account: it calls the Zitadel APIs ───────────

# service_account NAME ROLE DESCRIPTION — a machine user in the IAM admin's
# organization with an instance role; prints a new personal access token (the
# old ones are removed). The admin app checks the super-admin role of the
# signed-in user itself.
service_account() {
  local name=$1 role=$2 description=$3 id count token_id token
  id=$(zitadel POST /management/v1/users/_search "$(jq -nc --arg u "${name}" \
    '{queries: [{userNameQuery: {userName: $u, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" \
    | jq -r '.result[0].id // empty')
  if [[ -z "${id}" ]]; then
    id=$(zitadel POST /management/v1/users/machine "$(jq -nc --arg u "${name}" --arg d "${description}" \
      '{userName: $u, name: $u, description: $d}')" | jq -r '.userId')
    echo "Created machine user ${name} (${id})" >&2
  else
    echo "Reusing machine user ${name} (${id})" >&2
  fi
  count=$(zitadel POST /admin/v1/members/_search "$(jq -nc --arg u "${id}" \
    '{queries: [{userIdQuery: {userId: $u}}]}')" | jq '.result // [] | length')
  if [[ "${count}" == "0" ]]; then
    zitadel POST /admin/v1/members "$(jq -nc --arg u "${id}" --arg r "${role}" \
      '{userId: $u, roles: [$r]}')" >/dev/null
    echo "  gave ${name} the instance role ${role}" >&2
  fi
  for token_id in $(zitadel POST "/management/v1/users/${id}/pats/_search" '{}' | jq -r '.result[]?.id'); do
    zitadel DELETE "/management/v1/users/${id}/pats/${token_id}" >/dev/null
  done
  token=$(zitadel POST "/management/v1/users/${id}/pats" "$(jq -nc \
    '{expirationDate: (now + 365 * 24 * 3600 | todate)}')" | jq -r '.token')
  if [[ -z "${token}" || "${token}" == "null" ]]; then
    echo "ERROR: creating the token of ${name} failed." >&2
    return 1
  fi
  echo "${token}"
}

sa_token=$(service_account "${ADMIN_SA}" "${ADMIN_SA_ROLE}" \
  "apps/admin manages organizations, users and role assignments as this user")
instance_token=$(service_account "${INSTANCE_SA}" "${INSTANCE_SA_ROLE}" \
  "apps/admin changes the platform (instance) branding as this user, and nothing else")

# The admin app reads its client and session key from admin-oidc and the
# service accounts' tokens from admin-zitadel (k8s/apps/admin). A new session
# key signs everyone out, which is fine for test users.
kubectl create namespace admin --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n admin create secret generic admin-oidc \
  --from-literal=CLIENT_ID="${admin_client_id}" --from-literal=CLIENT_SECRET="${admin_client_secret}" \
  --from-literal=SESSION_KEY="$(openssl rand -hex 32)" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n admin create secret generic admin-zitadel --from-literal=ZITADEL_TOKEN="${sa_token}" \
  --from-literal=ZITADEL_INSTANCE_TOKEN="${instance_token}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
if kubectl -n admin get deployment admin &>/dev/null; then
  kubectl -n admin rollout restart deployment/admin >/dev/null
fi

admin_json=$(jq -nc --arg url "${ADMIN_URL}" --arg role "${ADMIN_ROLE}" --arg vrole "${VIEWER_ROLE}" \
  --arg sp "${super_admin_password}" --arg vp "${viewer_password}" --arg rp "${regular_user_password}" \
  '{url: $url, role: $role, users: [{username: "super-admin", password: $sp, roles: [$role]},
                                    {username: "viewer", password: $vp, roles: [$vrole]},
                                    {username: "regular-user", password: $rp, roles: []}]}')

jq -n --arg issuer "${ISSUER}" --arg gw "${GATEWAY}" --arg aud "${project_id}" --argjson t "${tenants_json}" \
  --argjson nt "${non_tenants_json}" --argjson admin "${admin_json}" \
  '{issuer: $issuer, gateway: $gw, audience: $aud, tenants: $t, non_tenants: $nt, admin: $admin}' >"${OUT}"
chmod 600 "${OUT}"
echo ""
echo "Wrote ${OUT}. Super admin page: ${ADMIN_URL} (users: jq .admin.users ${OUT})"
echo "Run the e2e tests:"
echo "  go test -C ${SCRIPT_DIR}/e2e -count=1 ./..."
