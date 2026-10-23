#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
CLUSTER_NAME="cngp-demo"
EXAMPLEAPP_IMAGE="hamsp/exampleapp:dev"
# ArgoCD deploys prototype/k8s/apps from GitHub at this revision. It must be pushed.
GITOPS_REVISION="${GITOPS_REVISION:-$(git -C "${REPO_DIR}" rev-parse --abbrev-ref HEAD)}"

# ── helpers ────────────────────────────────────────────────────────────────────

wait_for_api_server() {
  echo "Waiting for API server to become reachable..."
  local attempts=0 max=36  # 36 × 5s = 3 min
  until kubectl cluster-info &>/dev/null; do
    attempts=$((attempts + 1))
    if [[ ${attempts} -ge ${max} ]]; then
      echo "ERROR: API server did not become reachable after $((max * 5))s."
      exit 1
    fi
    echo "  (${attempts}/${max}) not ready yet, waiting 5s..."
    sleep 5
  done
  echo "  API server is up."
}

check_gitops_revision() {
  local remote_sha local_sha
  remote_sha=$(git -C "${REPO_DIR}" ls-remote origin "refs/heads/${GITOPS_REVISION}" | cut -f1)
  if [[ -z "${remote_sha}" ]]; then
    echo "ERROR: branch '${GITOPS_REVISION}' is not on origin. Push it or set GITOPS_REVISION."
    exit 1
  fi
  local_sha=$(git -C "${REPO_DIR}" rev-parse HEAD)
  if [[ "${remote_sha}" != "${local_sha}" ]]; then
    echo "WARNING: origin/${GITOPS_REVISION} (${remote_sha:0:7}) differs from local HEAD (${local_sha:0:7})."
    echo "         ArgoCD deploys what is on origin."
  fi
}

check_gitops_revision

# ── 1. clean up any stale cluster ─────────────────────────────────────────────

if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo "Removing stale cluster '${CLUSTER_NAME}'..."
  kind delete cluster --name "${CLUSTER_NAME}"
fi

# ── 2. create cluster ─────────────────────────────────────────────────────────

echo "Creating kind cluster '${CLUSTER_NAME}' (no CNI, no kube-proxy)..."
kind create cluster \
  --config "${SCRIPT_DIR}/kind-config.yaml" \
  --name  "${CLUSTER_NAME}" \
  --image kindest/node:v1.35.0 -v=7

wait_for_api_server

# ── 3. install Cilium (replaces kube-proxy) ───────────────────────────────────

# envoyConfig.enabled turns on CiliumEnvoyConfig, which runs the API Gateway
# (prototype/k8s/apps/gateway/envoy-config.yaml).
echo "Installing Cilium CNI v1.19.5..."
helm repo add cilium https://helm.cilium.io/ 2>/dev/null || true
helm repo update cilium

# Cilium with kubeProxyReplacement needs the real API server IP inside Docker,
# not the 127.0.0.1 port-forward that kind puts in kubeconfig.
API_SERVER_IP=$(docker inspect \
  -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' \
  "${CLUSTER_NAME}-control-plane")

helm upgrade --install cilium cilium/cilium \
  --version 1.19.5 \
  --namespace kube-system \
  --set kubeProxyReplacement=true \
  --set k8sServiceHost="${API_SERVER_IP}" \
  --set k8sServicePort=6443 \
  --set envoyConfig.enabled=true \
  --wait \
  --timeout 5m

echo "Waiting for all nodes to be Ready (requires Cilium)..."
kubectl wait --for=condition=Ready nodes --all --timeout=5m

# ── 4. install ArgoCD ─────────────────────────────────────────────────────────

echo "Installing ArgoCD chart v10.3.0..."
helm repo add argo https://argoproj.github.io/argo-helm 2>/dev/null || true
helm repo update argo

helm upgrade --install argocd argo/argo-cd \
  --version 10.3.0 \
  --namespace argocd \
  --create-namespace \
  --wait \
  --timeout 5m

# ── 5. build ExampleApp and load it into kind ─────────────────────────────────

echo "Building ${EXAMPLEAPP_IMAGE}..."
docker build -t "${EXAMPLEAPP_IMAGE}" "${REPO_DIR}/apps/exampleapp"
kind load docker-image "${EXAMPLEAPP_IMAGE}" --name "${CLUSTER_NAME}"

# ── 6. apply GitOps ApplicationSet ────────────────────────────────────────────
# CNPG, MinIO, the Postgres cluster, Zitadel, ExampleApp and the API Gateway.

echo "Applying ArgoCD ApplicationSet (revision ${GITOPS_REVISION})..."
sed -E "s#^( *(target)?[rR]evision:) master\$#\1 ${GITOPS_REVISION}#" \
  "${SCRIPT_DIR}/k8s/apps-applicationset.yaml" | kubectl apply -f -

# ── done ──────────────────────────────────────────────────────────────────────

echo ""
echo "==================================================================="
echo " Cluster '${CLUSTER_NAME}' is ready."
echo "==================================================================="
echo ""
echo "ArgoCD admin password:"
kubectl -n argocd get secret argocd-initial-admin-secret \
  -o jsonpath='{.data.password}' | base64 -d
echo ""
echo ""
echo "Access ArgoCD UI (run in a separate terminal):"
echo "  kubectl port-forward svc/argocd-server -n argocd 8080:443"
echo "  https://localhost:8080  (user: admin)"
echo ""
echo "Watch CNPG cluster come up:"
echo "  kubectl get cluster -n database -w"
echo ""
echo "Watch all ArgoCD apps:"
echo "  kubectl get applications -n argocd -w"
echo ""
echo "Then create the two test tenants and run the e2e tests:"
echo "  ${SCRIPT_DIR}/bootstrap-tenants.sh"
echo "  go test -C ${SCRIPT_DIR}/e2e -count=1 ./..."
