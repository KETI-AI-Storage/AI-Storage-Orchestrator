#!/bin/bash

# AI Storage Orchestrator Loadbalancing Test Script
# This script tests the loadbalancing functionality

set -e

NAMESPACE="default"
ORCHESTRATOR_PORT=8080

echo "=========================================="
echo "AI Storage Orchestrator Loadbalancing Test"
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

# Check if jq is available (optional but helpful)
if ! command -v jq &> /dev/null; then
    print_info "jq is not installed. Install it for better JSON formatting"
    USE_JQ=false
else
    USE_JQ=true
fi

# Step 1: Check if orchestrator is running
print_step "Checking if AI Storage Orchestrator is running..."
if kubectl get pods -n kube-system -l app=ai-storage-orchestrator | grep -q Running; then
    print_success "AI Storage Orchestrator is running"
else
    print_error "AI Storage Orchestrator is not running"
    exit 1
fi

# Step 2: Check cluster nodes
print_step "Checking cluster nodes..."
NODE_COUNT=$(kubectl get nodes --no-headers | wc -l)
print_info "Found $NODE_COUNT nodes in the cluster"

if [ "$NODE_COUNT" -lt 2 ]; then
    print_error "Loadbalancing requires at least 2 nodes. Found: $NODE_COUNT"
    exit 1
fi

kubectl get nodes -o wide

# Step 3: Setup port-forward to orchestrator
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

# Step 4: Check current cluster state
print_step "Analyzing current cluster state..."
RESPONSE=$(curl -s http://localhost:$ORCHESTRATOR_PORT/api/v1/loadbalancing/metrics)

if [ "$USE_JQ" = true ]; then
    echo "$RESPONSE" | jq '.'
else
    echo "$RESPONSE"
fi

# Step 5: Create test workloads (if not exist)
print_step "Ensuring test workloads exist..."

# Create a simple nginx deployment for testing
kubectl apply -f - <<EOF > /dev/null 2>&1 || true
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-test
  namespace: $NAMESPACE
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx-test
  template:
    metadata:
      labels:
        app: nginx-test
    spec:
      containers:
      - name: nginx
        image: nginx:latest
        resources:
          requests:
            cpu: "100m"
            memory: "128Mi"
EOF

print_info "Waiting for nginx-test deployment to be ready..."
kubectl wait --for=condition=available --timeout=60s deployment/nginx-test -n $NAMESPACE

# Step 6: Start loadbalancing (dry-run first)
print_step "Starting loadbalancing job (dry-run mode)..."
LB_RESPONSE=$(curl -s -X POST http://localhost:$ORCHESTRATOR_PORT/api/v1/loadbalancing \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "'$NAMESPACE'",
    "strategy": "load_spreading",
    "cpu_threshold": 80,
    "memory_threshold": 80,
    "max_migrations_per_cycle": 3,
    "interval": 0
  }')

if [ "$USE_JQ" = true ]; then
    LB_ID=$(echo $LB_RESPONSE | jq -r '.loadbalancing_id')
else
    LB_ID=$(echo $LB_RESPONSE | grep -o '"loadbalancing_id":"[^"]*"' | cut -d'"' -f4)
fi

if [ -z "$LB_ID" ]; then
    print_error "Failed to create loadbalancing job"
    echo "Response: $LB_RESPONSE"
    kill $PORT_FORWARD_PID 2>/dev/null || true
    exit 1
fi

print_success "Loadbalancing job created: $LB_ID (dry-run mode)"
echo ""

# Step 7: Monitor loadbalancing job
print_step "Monitoring loadbalancing job..."
print_info "Waiting for analysis to complete..."

for i in {1..10}; do
    sleep 2
    STATUS_RESPONSE=$(curl -s http://localhost:$ORCHESTRATOR_PORT/api/v1/loadbalancing/$LB_ID)

    if [ "$USE_JQ" = true ]; then
        STATUS=$(echo $STATUS_RESPONSE | jq -r '.status')
        echo "  Check #$i: Status = $STATUS"
    else
        STATUS=$(echo $STATUS_RESPONSE | grep -o '"status":"[^"]*"' | cut -d'"' -f4)
        echo "  Check #$i: Status = $STATUS"
    fi

    if [ "$STATUS" = "completed" ] || [ "$STATUS" = "failed" ]; then
        break
    fi
done

echo ""
print_step "Loadbalancing job results (dry-run):"
if [ "$USE_JQ" = true ]; then
    echo "$STATUS_RESPONSE" | jq '.details'
else
    echo "$STATUS_RESPONSE"
fi

# Extract key metrics
if [ "$USE_JQ" = true ]; then
    PODS_ANALYZED=$(echo $STATUS_RESPONSE | jq -r '.details.total_pods_analyzed // 0')
    PODS_TO_MIGRATE=$(echo $STATUS_RESPONSE | jq -r '.details.pods_to_migrate // 0')
    BALANCE_SCORE=$(echo $STATUS_RESPONSE | jq -r '.details.initial_state.balance_score // 0')

    echo ""
    print_info "Summary:"
    echo "  Total pods analyzed: $PODS_ANALYZED"
    echo "  Pods that would be migrated: $PODS_TO_MIGRATE"
    echo "  Current balance score: $BALANCE_SCORE/100"
fi

# Step 8: Run actual loadbalancing (if user confirms)
echo ""
read -p "Do you want to run actual loadbalancing (not dry-run)? (y/N): " -n 1 -r
echo ""

if [[ $REPLY =~ ^[Yy]$ ]]; then
    print_step "Starting actual loadbalancing job..."

    LB_RESPONSE2=$(curl -s -X POST http://localhost:$ORCHESTRATOR_PORT/api/v1/loadbalancing \
      -H "Content-Type: application/json" \
      -d '{
        "namespace": "'$NAMESPACE'",
        "strategy": "load_spreading",
        "cpu_threshold": 70,
        "memory_threshold": 70,
        "max_migrations_per_cycle": 2,
        "preserve_pv": true,
        "interval": 0
      }')

    if [ "$USE_JQ" = true ]; then
        LB_ID2=$(echo $LB_RESPONSE2 | jq -r '.loadbalancing_id')
    else
        LB_ID2=$(echo $LB_RESPONSE2 | grep -o '"loadbalancing_id":"[^"]*"' | cut -d'"' -f4)
    fi

    print_success "Loadbalancing job created: $LB_ID2"
    print_info "This may take several minutes..."

    # Monitor execution
    for i in {1..30}; do
        sleep 5
        STATUS_RESPONSE2=$(curl -s http://localhost:$ORCHESTRATOR_PORT/api/v1/loadbalancing/$LB_ID2)

        if [ "$USE_JQ" = true ]; then
            STATUS=$(echo $STATUS_RESPONSE2 | jq -r '.status')
            echo "  Progress check #$i: Status = $STATUS"
        else
            STATUS=$(echo $STATUS_RESPONSE2 | grep -o '"status":"[^"]*"' | cut -d'"' -f4)
            echo "  Progress check #$i: Status = $STATUS"
        fi

        if [ "$STATUS" = "completed" ] || [ "$STATUS" = "failed" ]; then
            break
        fi
    done

    echo ""
    print_step "Final loadbalancing results:"
    if [ "$USE_JQ" = true ]; then
        echo "$STATUS_RESPONSE2" | jq '.'
    else
        echo "$STATUS_RESPONSE2"
    fi
fi

# Step 9: List all loadbalancing jobs
echo ""
print_step "Listing all loadbalancing jobs..."
ALL_JOBS=$(curl -s http://localhost:$ORCHESTRATOR_PORT/api/v1/loadbalancing)
if [ "$USE_JQ" = true ]; then
    echo "$ALL_JOBS" | jq '.'
else
    echo "$ALL_JOBS"
fi

echo ""
echo "=========================================="
print_success "Loadbalancing test completed!"
echo "=========================================="
echo ""
print_info "To continue monitoring:"
echo "  curl http://localhost:$ORCHESTRATOR_PORT/api/v1/loadbalancing/$LB_ID"
echo ""
print_info "To view cluster state:"
echo "  kubectl get pods -A -o wide"
echo "  kubectl top nodes"
echo ""
print_info "To stop port-forward:"
echo "  kill $PORT_FORWARD_PID"
echo ""
