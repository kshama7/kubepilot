#!/usr/bin/env bash
#
# kind-up.sh — create a local kind cluster for KubePilot and write a kubeconfig
# the API container can use.
#
# Requires: kind (https://kind.sigs.k8s.io), kubectl, docker.
# Install kind on macOS with: brew install kind
#
# It produces ./.kube/config (relative to repo root) with the control-plane
# address rewritten to host.docker.internal so the API container can reach it.
set -euo pipefail

CLUSTER_NAME="kubepilot-dev"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KIND_CONFIG="${REPO_ROOT}/k8s/kind-cluster.yaml"
KUBE_DIR="${REPO_ROOT}/.kube"
KUBECONFIG_OUT="${KUBE_DIR}/config"

if ! command -v kind >/dev/null 2>&1; then
  echo "error: kind is not installed. Install it with: brew install kind" >&2
  exit 1
fi

if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  echo "kind cluster '${CLUSTER_NAME}' already exists; reusing it."
else
  echo "creating kind cluster '${CLUSTER_NAME}'..."
  kind create cluster --name "${CLUSTER_NAME}" --config "${KIND_CONFIG}"
fi

mkdir -p "${KUBE_DIR}"

# Export an internal kubeconfig (server address suitable for container access),
# then rewrite the host to host.docker.internal so the compose container reaches
# the kind API server published on the host.
kind get kubeconfig --name "${CLUSTER_NAME}" --internal >"${KUBECONFIG_OUT}.tmp"

# kind --internal points at the control-plane container's name on the kind
# network; for docker-compose with host-gateway we instead target the published
# host port. Derive it from the standard host kubeconfig.
kind get kubeconfig --name "${CLUSTER_NAME}" \
  | sed -E 's#server: https://127\.0\.0\.1:#server: https://host.docker.internal:#' \
  >"${KUBECONFIG_OUT}"
rm -f "${KUBECONFIG_OUT}.tmp"

echo
echo "kind cluster ready."
echo "  host kubectl:     kind export kubeconfig --name ${CLUSTER_NAME} && kubectl get nodes"
echo "  container config: ${KUBECONFIG_OUT} (server -> host.docker.internal)"
echo
echo "Start the API against it:"
echo "  KUBEPILOT_KUBECONFIG_HOST=${KUBECONFIG_OUT} docker compose up --build"
echo "  curl -s localhost:8080/api/v1/clusters/${CLUSTER_NAME}/health | jq"
