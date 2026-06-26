#!/usr/bin/env bash
#
# kind-down.sh — tear down the KubePilot local kind cluster and its kubeconfig.
set -euo pipefail

CLUSTER_NAME="kubepilot-dev"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if command -v kind >/dev/null 2>&1 && kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  kind delete cluster --name "${CLUSTER_NAME}"
fi
rm -f "${REPO_ROOT}/.kube/config"
echo "kind cluster '${CLUSTER_NAME}' removed."
