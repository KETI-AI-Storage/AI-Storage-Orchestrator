#!/bin/bash

set -e

echo "AI Storage Orchestrator 배포 스크립트"
echo "논문 기반 Pod 마이그레이션 오케스트레이터 배포"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
NAMESPACE="kube-system"
DEPLOYMENT_FILE="deployments/cluster-orchestrator.yaml"
IMAGE_NAME="ai-storage-orchestrator"
TAG=${1:-latest}

echo -e "${YELLOW}Deploying AI Storage Orchestrator to Kubernetes...${NC}"

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    echo -e "${RED}kubectl is not installed. Please install kubectl first.${NC}"
    exit 1
fi

# Check if cluster is accessible
echo "Checking Kubernetes cluster accessibility..."
if ! kubectl cluster-info &> /dev/null; then
    echo -e "${RED}Cannot connect to Kubernetes cluster. Please check your kubeconfig.${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Kubernetes cluster is accessible${NC}"

# Check and setup node labels
echo "Checking and setting up node labels..."

# Get control plane node
MASTER_NODE=$(kubectl get nodes -l node-role.kubernetes.io/control-plane --no-headers | awk '{print $1}' | head -n1)
if [ ! -z "$MASTER_NODE" ]; then
    echo "Labeling master node: $MASTER_NODE"
    kubectl label nodes $MASTER_NODE layer=orchestration --overwrite >/dev/null 2>&1
fi

# Get worker nodes and label at least one
WORKER_NODES=$(kubectl get nodes -l node-role.kubernetes.io/worker --no-headers | awk '{print $1}')
if [ ! -z "$WORKER_NODES" ]; then
    FIRST_WORKER=$(echo "$WORKER_NODES" | head -n1)
    echo "Labeling worker node: $FIRST_WORKER"
    kubectl label nodes $FIRST_WORKER layer=orchestration --overwrite >/dev/null 2>&1
fi

# Verify orchestration nodes
ORCHESTRATION_NODES=$(kubectl get nodes -l layer=orchestration --no-headers 2>/dev/null | wc -l)
if [ $ORCHESTRATION_NODES -gt 0 ]; then
    echo -e "${GREEN}✓ Found $ORCHESTRATION_NODES node(s) with orchestration label${NC}"
else
    echo -e "${RED}✗ No nodes available for orchestration${NC}"
    exit 1
fi

# Check if image exists in containerd
echo "Checking container image availability..."
IMAGE_EXISTS=$(sudo ctr -n k8s.io image ls | grep ai-storage-orchestrator:latest || echo "")

if [ -z "$IMAGE_EXISTS" ]; then
    echo -e "${YELLOW}⚠ Image not found in containerd, attempting to import from Docker...${NC}"
    
    # Check if Docker image exists
    DOCKER_IMAGE_EXISTS=$(docker images ai-storage-orchestrator:latest -q)
    
    if [ ! -z "$DOCKER_IMAGE_EXISTS" ]; then
        echo "Importing Docker image to containerd..."
        docker save ai-storage-orchestrator:latest | sudo ctr -n k8s.io image import -
        
        if [ $? -eq 0 ]; then
            echo -e "${GREEN}✓ Image imported to containerd${NC}"
        else
            echo -e "${RED}✗ Failed to import image${NC}"
            exit 1
        fi
    else
        echo -e "${RED}✗ Docker image not found. Please run ./scripts/build.sh first${NC}"
        exit 1
    fi
else
    echo -e "${GREEN}✓ Image found in containerd${NC}"
fi

# Apply the deployment
echo "Applying Kubernetes manifests..."
kubectl apply -f $DEPLOYMENT_FILE

if [ $? -ne 0 ]; then
    echo -e "${RED}Failed to apply Kubernetes manifests${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Kubernetes manifests applied successfully${NC}"

# Wait for deployment to be ready
echo "Waiting for deployment to be ready..."
kubectl rollout status deployment/ai-storage-orchestrator -n $NAMESPACE --timeout=300s

if [ $? -ne 0 ]; then
    echo -e "${RED}Deployment failed to become ready${NC}"
    echo "Checking pod status..."
    kubectl get pods -n $NAMESPACE -l app=ai-storage-orchestrator
    echo ""
    echo "Recent events:"
    kubectl get events -n $NAMESPACE --sort-by='.lastTimestamp' | tail -10
    exit 1
fi

echo -e "${GREEN}✓ Deployment is ready${NC}"

# Get service information
echo ""
echo -e "${BLUE}=== Deployment Information ===${NC}"
kubectl get all -n $NAMESPACE -l app=ai-storage-orchestrator

echo ""
echo -e "${BLUE}=== Pod Logs ===${NC}"
kubectl logs -n $NAMESPACE -l app=ai-storage-orchestrator --tail=10

echo ""
echo -e "${GREEN}Deployment completed successfully!${NC}"
echo ""
echo "Service endpoints:"
SERVICE_IP=$(kubectl get svc ai-storage-orchestrator -n $NAMESPACE -o jsonpath='{.spec.clusterIP}')
echo "- Cluster IP: $SERVICE_IP:8080"
echo "- Health check: http://$SERVICE_IP:8080/health"
echo "- Migration API: http://$SERVICE_IP:8080/api/v1/migrations"
echo "- Metrics: http://$SERVICE_IP:8080/api/v1/metrics"
echo ""
echo "To test the orchestrator:"
echo "1. Port forward: kubectl port-forward -n $NAMESPACE svc/ai-storage-orchestrator 8080:8080"
echo "2. Health check: curl http://localhost:8080/health"
echo "3. View logs: kubectl logs -n $NAMESPACE -l app=ai-storage-orchestrator -f"
echo ""
echo "Example migration request:"
cat << 'EOF'
curl -X POST http://localhost:8080/api/v1/migrations \
  -H "Content-Type: application/json" \
  -d '{
    "pod_name": "example-pod",
    "pod_namespace": "default", 
    "source_node": "node1",
    "target_node": "node2",
    "preserve_pv": true,
    "timeout": 600
  }'
EOF
