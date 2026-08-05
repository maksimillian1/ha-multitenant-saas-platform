#!/bin/bash
set -e

echo "Creating kind cluster with Cilium..."
kind create cluster --config kind-config.yaml --name cngp-demo --image kindest/node:v1.35.0 -v=7 || true

echo "Installing Cilium CNI (v1.19.5)..."
helm repo add cilium https://helm.cilium.io/ || true
helm repo update cilium

# When kube-proxy is disabled, Cilium needs to know the API server address
API_SERVER_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cngp-demo-control-plane)

helm upgrade --install cilium cilium/cilium --version 1.19.5 \
   --namespace kube-system \
   --set kubeProxyReplacement=true \
   --set k8sServiceHost=${API_SERVER_IP} \
   --set k8sServicePort=6443

echo "Waiting for Cilium to be ready..."
sleep 5
kubectl -n kube-system wait --for=condition=Ready pod -l k8s-app=cilium --timeout=300s || true

echo "Installing ArgoCD (chart v7.0.0)..."
helm repo add argo https://argoproj.github.io/argo-helm || true
helm repo update argo
helm upgrade --install argocd argo/argo-cd --version 7.0.0 \
   --namespace argocd --create-namespace

echo "Waiting for ArgoCD server..."
sleep 5
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=argocd-server -n argocd --timeout=300s || true

echo "Cluster is ready! You can now apply ArgoCD applications."
