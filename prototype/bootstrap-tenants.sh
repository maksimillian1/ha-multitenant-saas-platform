#!/bin/bash
# Creates the two test tenants of the walking skeleton (HAMSP-5): one Zitadel
# Organization per tenant with one machine user that gets tokens through
# client_credentials. Also creates the platform API project, whose ID is the
# audience the API Gateway requires (HAMSP-22), and hands that ID to the
# Gateway. For the super admin app (apps/admin) it creates the platform-admin
# project with the super-admin and viewer roles, an OIDC web app and three
# human test users, super-admin, viewer and regular-user (no role), and writes
# the app's client to the Secret admin/admin-oidc. It also creates the app's
# service account admin-app with the instance roles IAM_ORG_MANAGER and
# IAM_BRANDING_MANAGER (our role, prototype/k8s/apps/zitadel/values.yaml),
# which the app uses to manage organizations, users, role assignments and
# branding (HAMSP-34, HAMSP-37, HAMSP-36), and writes its JWT profile key to
# the Secret admin/admin-zitadel (key.json). Writes the credentials to
# prototype/.tenants.json (git-ignored) for the e2e tests and for signing in.
#
# It works as the Zitadel chart's machine user iam-admin (IAM_OWNER), with
# short-lived tokens from its key in the Secret zitadel/iam-admin. Keys live
# 30 days (HAMSP-36): every run gives iam-admin and admin-app a new key and
# deletes their other keys and any personal access token, including the
# chart's iam-admin-pat. A cluster on which this script has not run for 30
# days has no valid key left: recreate it.
#
# Safe to run again: existing organizations, users and projects are reused;
# client secrets, the keys and the admin test users' passwords are
# regenerated.
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
# IAM_BRANDING_MANAGER adds the instance policies (the platform branding)
# without the rest of IAM_OWNER.
ADMIN_SA_ROLES='["IAM_BRANDING_MANAGER","IAM_ORG_MANAGER"]'
# Until HAMSP-36 the platform branding used a second account with IAM_OWNER.
# It is deleted when found.
OLD_INSTANCE_SA="admin-app-instance"
# The setup job of the Zitadel chart creates this IAM_OWNER machine user.
IAM_ADMIN="iam-admin"
# How long a service account key lives.
KEY_DAYS=30

# Access tokens live 60 s, so the e2e tests can prove that an expired token is
# rejected (HAMSP-5 AC-5) without waiting hours.
ACCESS_TOKEN_LIFETIME="60s"  # protobuf Duration: seconds only

# ── helpers ────────────────────────────────────────────────────────────────────

b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

# key_token KEYFILE_JSON — an access token for the machine user of a JWT
# profile key, by the JWT bearer grant (RFC 7523). The key stays in memory.
key_token() {
  local key_json=$1 kid uid header payload sig
  kid=$(jq -r .keyId <<<"${key_json}")
  uid=$(jq -r .userId <<<"${key_json}")
  header=$(jq -nc --arg k "${kid}" '{alg: "RS256", typ: "JWT", kid: $k}' | b64url)
  payload=$(jq -nc --arg u "${uid}" --arg aud "${ISSUER}"     '{iss: $u, sub: $u, aud: $aud, iat: (now | floor), exp: ((now | floor) + 60)}' | b64url)
  sig=$(printf '%s.%s' "${header}" "${payload}"     | openssl dgst -sha256 -sign <(jq -r .key <<<"${key_json}") | b64url)
  curl -sS --fail-with-body --connect-to "${ZITADEL_HOST}:${GATEWAY}" -X POST "${ISSUER}/oauth/v2/token" \
    --data-urlencode "grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer" \
    --data-urlencode "scope=openid urn:zitadel:iam:org:project:id:zitadel:aud" \
    --data-urlencode "assertion=${header}.${payload}.${sig}" | jq -er .access_token
}

# admin_token — the IAM admin's current access token. Access tokens live 60 s
# on this instance (ACCESS_TOKEN_LIFETIME), so it gets a new one every 40 s.
TOKEN="" TOKEN_AT=0
admin_token() {
  if [[ -z "${TOKEN}" || $(( $(date +%s) - TOKEN_AT )) -ge 40 ]]; then
    TOKEN=$(key_token "${IAM_ADMIN_KEY}") || { echo "ERROR: no token for ${IAM_ADMIN}." >&2; return 1; }
    TOKEN_AT=$(date +%s)
  fi
}

# zitadel METHOD PATH [JSON] — calls the Zitadel API through the API Gateway as
# the IAM admin. Set ORG to act inside an organization.
zitadel() {
  admin_token
  local args=(-sS --fail-with-body --connect-to "${ZITADEL_HOST}:${GATEWAY}"
    -X "$1" "${ISSUER}$2"
    -H "Authorization: Bearer ${TOKEN}" -H "Content-Type: application/json")
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

wait_for "IAM admin key (zitadel setup job)" 120 \
  kubectl -n zitadel get secret "${IAM_ADMIN}"
IAM_ADMIN_KEY=$(kubectl -n zitadel get secret "${IAM_ADMIN}" -o jsonpath="{.data.${IAM_ADMIN}\.json}" | base64 -d)
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

# ── 6. the admin app's service account: it calls the Zitadel APIs ───────────

# new_key USER_ID — a new JSON key of a machine user, valid KEY_DAYS days;
# prints the key file. ORG must be the user's organization (or empty for the
# IAM admin's).
new_key() {
  zitadel POST "/management/v1/users/$1/keys" "$(jq -nc --argjson d "${KEY_DAYS}" \
    '{type: "KEY_TYPE_JSON", expirationDate: (now + $d * 24 * 3600 | todate)}')" \
    | jq -er .keyDetails | base64 -d
}

# drop_credentials USER_ID KEEP_KEY_ID — deletes every key of a machine user
# but KEEP_KEY_ID, and every personal access token.
drop_credentials() {
  local id=$1 keep=$2 key_id token_id
  for key_id in $(zitadel POST "/management/v1/users/${id}/keys/_search" '{}' | jq -r '.result[]?.id'); do
    [[ "${key_id}" == "${keep}" ]] || zitadel DELETE "/management/v1/users/${id}/keys/${key_id}" >/dev/null
  done
  for token_id in $(zitadel POST "/management/v1/users/${id}/pats/_search" '{}' | jq -r '.result[]?.id'); do
    zitadel DELETE "/management/v1/users/${id}/pats/${token_id}" >/dev/null
  done
}

# user_id NAME — the ID of a user of the IAM admin's organization, or empty.
user_id() {
  zitadel POST /management/v1/users/_search "$(jq -nc --arg u "$1" \
    '{queries: [{userNameQuery: {userName: $u, method: "TEXT_QUERY_METHOD_EQUALS"}}]}')" \
    | jq -r '.result[0].id // empty'
}

# The admin app's machine user, in the IAM admin's organization, with its
# instance roles. The admin app checks the super-admin role of the signed-in
# user itself.
sa_id=$(user_id "${ADMIN_SA}")
if [[ -z "${sa_id}" ]]; then
  sa_id=$(zitadel POST /management/v1/users/machine "$(jq -nc --arg u "${ADMIN_SA}" \
    '{userName: $u, name: $u, description: "apps/admin manages organizations, users, role assignments and branding as this user"}')" \
    | jq -r '.userId')
  echo "Created machine user ${ADMIN_SA} (${sa_id})"
else
  echo "Reusing machine user ${ADMIN_SA} (${sa_id})"
fi
member=$(zitadel POST /admin/v1/members/_search "$(jq -nc --arg u "${sa_id}" \
  '{queries: [{userIdQuery: {userId: $u}}]}')" | jq -c '.result[0].roles // empty | sort')
if [[ -z "${member}" ]]; then
  zitadel POST /admin/v1/members "$(jq -nc --arg u "${sa_id}" --argjson r "${ADMIN_SA_ROLES}" \
    '{userId: $u, roles: $r}')" >/dev/null
  echo "  gave ${ADMIN_SA} the instance roles ${ADMIN_SA_ROLES}"
elif [[ "${member}" != "$(jq -c 'sort' <<<"${ADMIN_SA_ROLES}")" ]]; then
  zitadel PUT "/admin/v1/members/${sa_id}" "$(jq -nc --argjson r "${ADMIN_SA_ROLES}" '{roles: $r}')" >/dev/null
  echo "  changed the instance roles of ${ADMIN_SA} from ${member} to ${ADMIN_SA_ROLES}"
fi
sa_key=$(new_key "${sa_id}")
drop_credentials "${sa_id}" "$(jq -r .keyId <<<"${sa_key}")"
echo "  new ${KEY_DAYS}-day key for ${ADMIN_SA}; its other keys and tokens are deleted"

# The IAM_OWNER account of the platform branding before HAMSP-36.
old_id=$(user_id "${OLD_INSTANCE_SA}")
if [[ -n "${old_id}" ]]; then
  zitadel DELETE "/v2/users/${old_id}" >/dev/null
  echo "Deleted machine user ${OLD_INSTANCE_SA} (${old_id})"
fi

# The admin app reads its client and session key from admin-oidc and its
# service account's key from admin-zitadel (k8s/apps/admin). A new session
# key signs everyone out, which is fine for test users.
kubectl create namespace admin --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n admin create secret generic admin-oidc \
  --from-literal=CLIENT_ID="${admin_client_id}" --from-literal=CLIENT_SECRET="${admin_client_secret}" \
  --from-literal=SESSION_KEY="$(openssl rand -hex 32)" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
# apply replaces the Secret's data, so the tokens of before HAMSP-36 go too.
kubectl -n admin create secret generic admin-zitadel --from-file=key.json=<(printf '%s' "${sa_key}") \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
if kubectl -n admin get deployment admin &>/dev/null; then
  kubectl -n admin rollout restart deployment/admin >/dev/null
fi

# ── 7. the IAM admin's own key ─────────────────────────────────────────────────

# A new key for iam-admin replaces the chart's (valid until 2029) and the one
# of the last run; the script goes on with the new key before it deletes the
# others. The chart's personal access token and its Secret go too.
iam_admin_id=$(jq -r .userId <<<"${IAM_ADMIN_KEY}")
iam_admin_key=$(new_key "${iam_admin_id}")
kubectl -n zitadel create secret generic "${IAM_ADMIN}" --from-file="${IAM_ADMIN}.json"=<(printf '%s' "${iam_admin_key}") \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
IAM_ADMIN_KEY=${iam_admin_key} TOKEN=""
drop_credentials "${iam_admin_id}" "$(jq -r .keyId <<<"${iam_admin_key}")"
kubectl -n zitadel delete secret "${IAM_ADMIN}-pat" --ignore-not-found >/dev/null
echo "New ${KEY_DAYS}-day key for ${IAM_ADMIN}; its other keys and tokens are deleted"

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
