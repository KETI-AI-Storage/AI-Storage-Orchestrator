#!/usr/bin/env bash
# Manta API stub test: PrepareDataset, ReleaseDatasetHint, ReportPodPlacement, ReportDatasetUsage
# Logs show workload_id, pod, namespace, node, dataset, job_type, etc. (job_type: Preprocessing)
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

echo "=========================================="
echo " Manta Stub API Test (Preprocessing)"
echo " Directory: $ROOT_DIR"
echo "=========================================="
echo ""

go test -v -run TestMantaStubLogs_RealPod ./pkg/manta/

echo ""
echo "=========================================="
echo " Test finished"
echo "=========================================="
