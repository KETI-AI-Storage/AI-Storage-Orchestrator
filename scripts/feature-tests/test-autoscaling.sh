#!/bin/bash

# AI Storage Orchestrator Autoscaling Test Script
# This script tests the autoscaling functionality with GPU workloads

set -e

NAMESPACE="default"
WORKLOAD_NAME="gpu-test-workload"
ORCHESTRATOR_PORT=8080

echo "=========================================="
echo "AI Storage Orchestrator Autoscaling Test"
echo "=========================================="
echo ""

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_step() {
    echo -e "${GREEN}[STEP]${NC} $1"
}

print_info() {
    echo -e "${YELLOW}[INFO]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    print_error "kubectl is not installed or not in PATH"
    exit 1
fi

# Check if curl is available
if ! command -v curl &> /dev/null; then
    print_error "curl is not installed or not in PATH"
    exit 1
fi

# Step 1: Check if orchestrator is running
print_step "Checking if AI Storage Orchestrator is running..."
if kubectl get pods -n kube-system -l app=ai-storage-orchestrator | grep -q Running; then
    print_success "AI Storage Orchestrator is running"
else
    print_error "AI Storage Orchestrator is not running"
    exit 1
fi

# Step 2: Check if DCGM Exporter is running
print_step "Checking if DCGM Exporter is running..."
if kubectl get pods -n gpu-monitoring -l app=dcgm-exporter | grep -q Running; then
    print_success "DCGM Exporter is running"
else
    print_error "DCGM Exporter is not running. Deploy it first:"
    echo "  kubectl apply -f deployments/dcgm-exporter.yaml"
    exit 1
fi

# Step 3: Deploy GPU test workload
print_step "Deploying GPU test workload..."
if kubectl get deployment $WORKLOAD_NAME -n $NAMESPACE &> /dev/null; then
    print_info "Workload already exists, skipping deployment"
else
    kubectl apply -f /root/workspace/test-gpu-workload.yaml
    print_info "Waiting for workload to be ready..."
    kubectl wait --for=condition=available --timeout=120s deployment/$WORKLOAD_NAME -n $NAMESPACE
    print_success "GPU test workload deployed"
fi

# Step 4: Check GPU node labels
print_step "Checking GPU node labels..."
GPU_NODES=$(kubectl get nodes -l nvidia.com/gpu=present --no-headers | wc -l)
if [ "$GPU_NODES" -eq 0 ]; then
    print_error "No GPU nodes found. Label your GPU nodes:"
    echo "  kubectl label nodes <node-name> nvidia.com/gpu=present"
    exit 1
else
    print_success "Found $GPU_NODES GPU node(s)"
fi

# Step 5: Setup port-forward to orchestrator
print_step "Setting up port-forward to orchestrator..."
# Kill any existing port-forward
pkill -f "kubectl port-forward.*ai-storage-orchestrator" || true
sleep 2

# Start new port-forward in background
kubectl port-forward -n kube-system svc/ai-storage-orchestrator $ORCHESTRATOR_PORT:8080 > /dev/null 2>&1 &
PORT_FORWARD_PID=$!
sleep 3

# Check if port-forward is working
if ! curl -s http://localhost:$ORCHESTRATOR_PORT/health > /dev/null; then
    print_error "Failed to connect to orchestrator"
    kill $PORT_FORWARD_PID 2>/dev/null || true
    exit 1
fi
print_success "Port-forward established (PID: $PORT_FORWARD_PID)"

# Step 6: Create autoscaler
print_step "Creating autoscaler for GPU workload..."
AUTOSCALER_RESPONSE=$(curl -s -X POST http://localhost:$ORCHESTRATOR_PORT/api/v1/autoscalers \
  -H "Content-Type: application/json" \
  -d "{
    \"workload_name\": \"$WORKLOAD_NAME\",
    \"workload_namespace\": \"$NAMESPACE\",
    \"workload_type\": \"deployment\",
    \"min_replicas\": 1,
    \"max_replicas\": 5,
    \"target_cpu_percent\": 70,
    \"target_memory_percent\": 80,
    \"target_gpu_percent\": 60,
    \"scale_check_interval\": 30
  }")

AUTOSCALER_ID=$(echo $AUTOSCALER_RESPONSE | grep -o '"autoscaling_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$AUTOSCALER_ID" ]; then
    print_error "Failed to create autoscaler"
    echo "Response: $AUTOSCALER_RESPONSE"
    kill $PORT_FORWARD_PID 2>/dev/null || true
    exit 1
fi

print_success "Autoscaler created with ID: $AUTOSCALER_ID"
echo ""

# Step 7: Monitor autoscaler for 3 minutes
print_step "Monitoring autoscaler for 3 minutes..."
print_info "Autoscaler will check metrics every 30 seconds"
echo ""

for i in {1..6}; do
    echo "----------------------------------------"
    echo "Check #$i ($(date '+%H:%M:%S'))"
    echo "----------------------------------------"

    # Get autoscaler status
    STATUS_RESPONSE=$(curl -s http://localhost:$ORCHESTRATOR_PORT/api/v1/autoscalers/$AUTOSCALER_ID)

    # Extract metrics using grep
    CURRENT_REPLICAS=$(echo $STATUS_RESPONSE | grep -o '"current_replicas":[0-9]*' | cut -d':' -f2)
    DESIRED_REPLICAS=$(echo $STATUS_RESPONSE | grep -o '"desired_replicas":[0-9]*' | cut -d':' -f2)
    CURRENT_CPU=$(echo $STATUS_RESPONSE | grep -o '"current_cpu_percent":[0-9]*' | cut -d':' -f2)
    CURRENT_MEMORY=$(echo $STATUS_RESPONSE | grep -o '"current_memory_percent":[0-9]*' | cut -d':' -f2)
    CURRENT_GPU=$(echo $STATUS_RESPONSE | grep -o '"current_gpu_percent":[0-9]*' | cut -d':' -f2)
    SCALE_UP_COUNT=$(echo $STATUS_RESPONSE | grep -o '"scale_up_count":[0-9]*' | cut -d':' -f2)
    SCALE_DOWN_COUNT=$(echo $STATUS_RESPONSE | grep -o '"scale_down_count":[0-9]*' | cut -d':' -f2)

    echo "  Replicas: $CURRENT_REPLICAS (desired: $DESIRED_REPLICAS)"
    echo "  CPU: ${CURRENT_CPU}% (target: 70%)"
    echo "  Memory: ${CURRENT_MEMORY}% (target: 80%)"
    echo "  GPU: ${CURRENT_GPU}% (target: 60%)"
    echo "  Scale events: UP=$SCALE_UP_COUNT, DOWN=$SCALE_DOWN_COUNT"
    echo ""

    if [ $i -lt 6 ]; then
        print_info "Waiting 30 seconds for next check..."
        sleep 30
    fi
done

echo ""
print_step "Final autoscaler status:"
curl -s http://localhost:$ORCHESTRATOR_PORT/api/v1/autoscalers/$AUTOSCALER_ID | grep -o '"status":"[^"]*"' | cut -d'"' -f4

echo ""
echo "=========================================="
print_success "Autoscaling test completed!"
echo "=========================================="
echo ""
print_info "To continue monitoring:"
echo "  watch -n 5 'curl -s http://localhost:$ORCHESTRATOR_PORT/api/v1/autoscalers/$AUTOSCALER_ID'"
echo ""
print_info "To delete autoscaler:"
echo "  curl -X DELETE http://localhost:$ORCHESTRATOR_PORT/api/v1/autoscalers/$AUTOSCALER_ID"
echo ""
print_info "To stop port-forward:"
echo "  kill $PORT_FORWARD_PID"
echo ""
