#!/bin/bash

# PV Checkpoint 확인 스크립트

set -e

NAMESPACE=${1:-"ai-migration-test-$(date +%s)"}
POD_NAME="tensorflow-ai-workload"

echo "========================================"
echo "   PV Checkpoint Verification Script"
echo "========================================"
echo "Namespace: $NAMESPACE"
echo ""

# 1. PVC 확인
echo "1. Checking PVC status..."
kubectl get pvc -n "$NAMESPACE" 2>/dev/null || echo "No PVC found in namespace $NAMESPACE"
echo ""

# 2. Pod 볼륨 마운트 확인
echo "2. Checking Pod volume mounts..."
kubectl get pod -n "$NAMESPACE" -o wide 2>/dev/null || echo "No pods found"
echo ""

# 3. 실제 데이터 확인
echo "3. Checking data in PV..."
ACTUAL_POD=$(kubectl get pods -n "$NAMESPACE" --no-headers -o custom-columns=":metadata.name" | head -n1)

if [ -n "$ACTUAL_POD" ]; then
    echo "Pod: $ACTUAL_POD"
    
    # 각 컨테이너에서 checkpoint 경로 확인
    CONTAINERS=$(kubectl get pod "$ACTUAL_POD" -n "$NAMESPACE" -o jsonpath='{.spec.containers[*].name}')
    
    for container in $CONTAINERS; do
        echo ""
        echo "Container: $container"
        echo "---"
        
        # /migration-checkpoint 경로 확인
        kubectl exec "$ACTUAL_POD" -n "$NAMESPACE" -c "$container" -- \
            sh -c "ls -lah /migration-checkpoint/ 2>/dev/null || echo 'No checkpoint path mounted'" 2>/dev/null || echo "Cannot access container $container"
    done
else
    echo "No pods found to check"
fi

echo ""
echo "4. Checking AI Orchestrator logs for PV usage..."
kubectl logs -n kube-system deployment/ai-storage-orchestrator --tail=50 2>/dev/null | \
    grep -i "pv\|checkpoint\|persist\|volume" || echo "No PV-related logs found"

echo ""
echo "5. PV/PVC Summary..."
kubectl get pv,pvc --all-namespaces | grep -i "tensorflow\|migration" || echo "No related PV/PVC found"

echo ""
echo "========================================"
echo "   Verification Complete"
echo "========================================"

