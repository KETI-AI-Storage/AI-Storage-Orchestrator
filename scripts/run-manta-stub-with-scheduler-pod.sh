#!/usr/bin/env bash
# 1) Apply test-scheduler-pod.yaml and wait for it to be scheduled and Ready.
# 2) Run the 4 Manta API stub test with that pod's name and node; log the 4 API results.
#
# Usage:
#   ./scripts/run-manta-stub-with-scheduler-pod.sh [path-to-pod-yaml]
#   Default pod yaml: ../../test-scheduler-pod.yaml (workspace root)
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
POD_YAML="${1:-$ROOT_DIR/../test-scheduler-pod.yaml}"
POD_NAME="test-scheduler-pod"
NAMESPACE="default"

cd "$ROOT_DIR"

echo "=========================================="
echo " 1) Apply pod and wait for Ready"
echo "    YAML: $POD_YAML"
echo "=========================================="
if [[ ! -f "$POD_YAML" ]]; then
  echo "Error: Pod YAML not found: $POD_YAML"
  exit 1
fi
echo "[INFO] Creating pod/$POD_NAME / namespace $NAMESPACE"
kubectl apply -f "$POD_YAML" >/dev/null
kubectl wait --for=condition=Ready "pod/$POD_NAME" -n "$NAMESPACE" --timeout=120s >/dev/null 2>&1 || true

echo ""
echo " 2) Get scheduled node"
NODE_NAME=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.nodeName}' 2>/dev/null || echo "worker-1")
if [[ -z "$NODE_NAME" ]]; then
  NODE_NAME="worker-1"
fi
echo "    pod=$POD_NAME namespace=$NAMESPACE node=$NODE_NAME"

echo ""
echo "=========================================="
echo " 3) Run 4 Manta API stubs (log results)"
echo "=========================================="
export TEST_POD_NAME="$POD_NAME"
export TEST_NAMESPACE="$NAMESPACE"
export TEST_NODE="$NODE_NAME"
go test -v -run TestMantaStubLogs_RealPod ./pkg/manta/

echo ""
echo "=========================================="
echo " Done"
echo "=========================================="
