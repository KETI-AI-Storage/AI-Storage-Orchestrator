#!/bin/bash

# GPU Load Simulation Script
# This script scales the GPU workload to simulate different load scenarios

set -e

NAMESPACE="default"
WORKLOAD_NAME="gpu-test-workload"

# Color codes
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

print_info() {
    echo -e "${YELLOW}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

echo "=========================================="
echo "GPU Load Simulator"
echo "=========================================="
echo ""

print_info "This script will simulate different GPU load scenarios by scaling the workload"
echo ""

# Scenario 1: Low load
print_info "Scenario 1: Low load (1 replica)"
kubectl scale deployment/$WORKLOAD_NAME -n $NAMESPACE --replicas=1
print_success "Scaled to 1 replica"
echo "Current GPU utilization should be low"
echo "Autoscaler may scale down if current load is below target"
echo ""
read -p "Press Enter to continue to next scenario..."
echo ""

# Scenario 2: Medium load
print_info "Scenario 2: Medium load (2 replicas)"
kubectl scale deployment/$WORKLOAD_NAME -n $NAMESPACE --replicas=2
print_success "Scaled to 2 replicas"
echo "Current GPU utilization should be moderate"
echo "Autoscaler should maintain current replicas if within target range"
echo ""
read -p "Press Enter to continue to next scenario..."
echo ""

# Scenario 3: High load
print_info "Scenario 3: High load (3 replicas)"
kubectl scale deployment/$WORKLOAD_NAME -n $NAMESPACE --replicas=3
print_success "Scaled to 3 replicas"
echo "Current GPU utilization may increase"
echo "Autoscaler may scale up if above target"
echo ""
read -p "Press Enter to reset to baseline..."
echo ""

# Reset to baseline
print_info "Resetting to baseline (1 replica)"
kubectl scale deployment/$WORKLOAD_NAME -n $NAMESPACE --replicas=1
print_success "Reset complete"
echo ""

print_info "Current pod status:"
kubectl get pods -n $NAMESPACE -l app=gpu-test -o wide
echo ""
