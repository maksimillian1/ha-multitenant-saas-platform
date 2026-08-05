#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER_NAME="cngp-demo"

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
  --wait \
  --timeout 5m

echo "Waiting for all nodes to be Ready (requires Cilium)..."
kubectl wait --for=condition=Ready nodes --all --timeout=5m

# ── 4. install ArgoCD ─────────────────────────────────────────────────────────

echo "Installing ArgoCD chart v7.0.0..."
helm repo add argo https://argoproj.github.io/argo-helm 2>/dev/null || true
helm repo update argo

helm upgrade --install argocd argo/argo-cd \
  --version 7.0.0 \
  --namespace argocd \
  --create-namespace \
  --wait \
  --timeout 5m

# ── 5. apply GitOps ApplicationSet (CNPG + MinIO + Postgres cluster) ──────────

echo "Applying ArgoCD ApplicationSet..."
kubectl apply -f "${SCRIPT_DIR}/gitops/apps-applicationset.yaml"

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
