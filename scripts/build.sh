#!/bin/bash

set -e

echo "AI Storage Orchestrator 빌드 스크립트"
echo "논문 기반 Pod 마이그레이션 오케스트레이터 빌드"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
IMAGE_NAME="ai-storage-orchestrator"
TAG=${1:-latest}
REGISTRY=${REGISTRY:-""}

echo -e "${YELLOW}Building AI Storage Orchestrator...${NC}"

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo -e "${RED}Go is not installed. Please install Go 1.21 or later.${NC}"
    exit 1
fi

# Check Go version
GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
REQUIRED_VERSION="1.21"

if [ "$(printf '%s\n' "$REQUIRED_VERSION" "$GO_VERSION" | sort -V | head -n1)" != "$REQUIRED_VERSION" ]; then
    echo -e "${RED}Go version $GO_VERSION is too old. Please install Go $REQUIRED_VERSION or later.${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Go version $GO_VERSION is compatible${NC}"

# Clean up previous builds
echo "Cleaning up previous builds..."
rm -f main

# Download dependencies
echo "Downloading Go dependencies..."
go mod tidy
go mod download

if [ $? -ne 0 ]; then
    echo -e "${RED}Failed to download dependencies${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Dependencies downloaded${NC}"

# Build the binary
echo "Building Go binary..."
CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags "-w -s" -o main ./cmd/main.go

if [ $? -ne 0 ]; then
    echo -e "${RED}Failed to build Go binary${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Go binary built successfully${NC}"

# Build Docker image
echo "Building Docker image..."
docker build -t ${IMAGE_NAME}:${TAG} .

if [ $? -ne 0 ]; then
    echo -e "${RED}Failed to build Docker image${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Docker image built: ${IMAGE_NAME}:${TAG}${NC}"

# Import image to containerd (for Kubernetes)
echo "Importing image to containerd..."
docker save ${IMAGE_NAME}:${TAG} | sudo ctr -n k8s.io image import -

if [ $? -eq 0 ]; then
    echo -e "${GREEN}✓ Image imported to containerd${NC}"
else
    echo -e "${YELLOW}⚠ Failed to import to containerd (manual import may be needed)${NC}"
fi

# Tag for registry if specified
if [ ! -z "$REGISTRY" ]; then
    echo "Tagging for registry: $REGISTRY"
    docker tag ${IMAGE_NAME}:${TAG} ${REGISTRY}/${IMAGE_NAME}:${TAG}
    echo -e "${GREEN}✓ Image tagged for registry: ${REGISTRY}/${IMAGE_NAME}:${TAG}${NC}"
fi

echo -e "${GREEN}Build completed successfully!${NC}"
echo ""
echo "Next steps:"
echo "1. Deploy to Kubernetes: kubectl apply -f deployments/cluster-orchestrator.yaml"
echo "2. Check status: kubectl get pods -n kube-system -l app=ai-storage-orchestrator"
echo "3. View logs: kubectl logs -n kube-system -l app=ai-storage-orchestrator -f"
echo ""
echo "API endpoints will be available at:"
echo "- POST /api/v1/migrations - Start pod migration"
echo "- GET /api/v1/migrations/:id - Get migration status" 
echo "- GET /api/v1/metrics - View performance metrics"
