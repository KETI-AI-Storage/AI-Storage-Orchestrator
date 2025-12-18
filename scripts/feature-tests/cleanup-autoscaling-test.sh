#!/bin/bash

# Cleanup script for autoscaling tests

set -e

NAMESPACE="default"
WORKLOAD_NAME="gpu-test-workload"
ORCHESTRATOR_PORT=8080

# Color codes
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

print_info() {
    echo -e "${YELLOW}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

echo "=========================================="
echo "Autoscaling Test Cleanup"
echo "=========================================="
echo ""

# Step 1: Delete all autoscalers
print_info "Deleting all autoscalers..."
if curl -s http://localhost:$ORCHESTRATOR_PORT/health > /dev/null 2>&1; then
    # Get all autoscaler IDs
    AUTOSCALERS=$(curl -s http://localhost:$ORCHESTRATOR_PORT/api/v1/autoscalers)
    AUTOSCALER_IDS=$(echo $AUTOSCALERS | grep -o '"autoscaling_id":"[^"]*"' | cut -d'"' -f4)

    if [ -z "$AUTOSCALER_IDS" ]; then
        print_info "No autoscalers found"
    else
        for ID in $AUTOSCALER_IDS; do
            print_info "Deleting autoscaler: $ID"
            curl -s -X DELETE http://localhost:$ORCHESTRATOR_PORT/api/v1/autoscalers/$ID > /dev/null
        done
        print_success "All autoscalers deleted"
    fi
else
    print_error "Cannot connect to orchestrator (is port-forward running?)"
    print_info "Skipping autoscaler deletion"
fi

# Step 2: Kill port-forward
print_info "Killing port-forward processes..."
pkill -f "kubectl port-forward.*ai-storage-orchestrator" || true
print_success "Port-forward processes terminated"

# Step 3: Delete GPU test workload (optional)
echo ""
read -p "Do you want to delete the GPU test workload? (y/N): " -n 1 -r
echo ""
if [[ $REPLY =~ ^[Yy]$ ]]; then
    print_info "Deleting GPU test workload..."
    kubectl delete deployment/$WORKLOAD_NAME -n $NAMESPACE --ignore-not-found=true
    print_success "GPU test workload deleted"
else
    print_info "Keeping GPU test workload"
fi

# Step 4: Delete DCGM Exporter (optional)
echo ""
read -p "Do you want to delete DCGM Exporter? (y/N): " -n 1 -r
echo ""
if [[ $REPLY =~ ^[Yy]$ ]]; then
    print_info "Deleting DCGM Exporter..."
    kubectl delete -f /root/workspace/ai-storage-orchestrator/deployments/dcgm-exporter.yaml --ignore-not-found=true
    print_success "DCGM Exporter deleted"
else
    print_info "Keeping DCGM Exporter"
fi

echo ""
print_success "Cleanup completed!"
echo ""
