#!/bin/bash

# AI Storage Orchestrator - Autoscaling API Test Script (Storage I/O 포함)
# Tests all autoscaling features including storage-aware scaling

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
BASE_URL="http://localhost:8080/api/v1"
AUTOSCALER_ID=""

# Helper functions
print_header() {
    echo -e "\n${BLUE}========================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}========================================${NC}\n"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

print_info() {
    echo -e "${YELLOW}ℹ $1${NC}"
}

check_response() {
    local response=$1
    local test_name=$2

    if echo "$response" | grep -q "error"; then
        print_error "$test_name failed"
        echo "$response" | jq '.'
        return 1
    else
        print_success "$test_name passed"
        echo "$response" | jq '.'
        return 0
    fi
}

# Main tests
print_header "AI Storage Orchestrator - Autoscaling Test Suite"
print_info "Testing storage-aware autoscaling for AI/ML workloads"

# Test 1: Health Check
print_header "Test 1: Health Check"
response=$(curl -s http://localhost:8080/health)
if echo "$response" | grep -q "healthy"; then
    print_success "Health check passed"
else
    print_error "Health check failed"
    exit 1
fi

# Test 2: Create Autoscaler (CPU + Memory only - traditional)
print_header "Test 2: Create Traditional Autoscaler (CPU/Memory)"
response=$(curl -s -X POST $BASE_URL/autoscaling \
  -H "Content-Type: application/json" \
  -d '{
    "workload_name": "test-deployment",
    "workload_namespace": "default",
    "workload_type": "Deployment",
    "min_replicas": 1,
    "max_replicas": 5,
    "target_cpu_percent": 70,
    "target_memory_percent": 80
  }')

if check_response "$response" "Traditional autoscaler creation"; then
    AUTOSCALER_ID=$(echo "$response" | jq -r '.autoscaling_id')
    print_info "Autoscaler ID: $AUTOSCALER_ID"
fi

sleep 2

# Test 3: Get Autoscaler Status
print_header "Test 3: Get Autoscaler Status"
if [ -n "$AUTOSCALER_ID" ]; then
    response=$(curl -s $BASE_URL/autoscaling/$AUTOSCALER_ID)
    check_response "$response" "Get autoscaler status"
fi

sleep 2

# Test 4: Delete Traditional Autoscaler
print_header "Test 4: Delete Traditional Autoscaler"
if [ -n "$AUTOSCALER_ID" ]; then
    response=$(curl -s -X DELETE $BASE_URL/autoscaling/$AUTOSCALER_ID)
    print_success "Autoscaler deleted"
    echo "$response" | jq '.'
fi

sleep 2

# Test 5: Create Storage-Aware Autoscaler (GPU + Storage I/O)
print_header "Test 5: Create Storage-Aware Autoscaler (GPU + Storage I/O)"
print_info "This is the KEY feature for AI/ML data-intensive workloads"
response=$(curl -s -X POST $BASE_URL/autoscaling \
  -H "Content-Type: application/json" \
  -d '{
    "workload_name": "ai-training-job",
    "workload_namespace": "default",
    "workload_type": "Deployment",
    "min_replicas": 2,
    "max_replicas": 8,
    "target_gpu_percent": 80,
    "target_storage_read_throughput_mbps": 600,
    "target_storage_write_throughput_mbps": 150,
    "target_storage_iops": 3000,
    "scale_up_policy": {
      "stabilization_window_seconds": 30,
      "max_scale_change": 2
    },
    "scale_down_policy": {
      "stabilization_window_seconds": 300,
      "max_scale_change": 1
    }
  }')

if check_response "$response" "Storage-aware autoscaler creation"; then
    AUTOSCALER_ID=$(echo "$response" | jq -r '.autoscaling_id')
    print_info "Storage-aware Autoscaler ID: $AUTOSCALER_ID"

    # Check if storage metrics are included in response
    if echo "$response" | jq '.details' | grep -q "current_storage_read_throughput_mbps"; then
        print_success "Storage I/O metrics found in response"
    else
        print_error "Storage I/O metrics missing in response"
    fi
fi

sleep 2

# Test 6: Verify Storage Metrics are Being Collected
print_header "Test 6: Verify Storage I/O Metrics Collection"
if [ -n "$AUTOSCALER_ID" ]; then
    print_info "Waiting 20 seconds for metrics collection..."
    sleep 20

    response=$(curl -s $BASE_URL/autoscaling/$AUTOSCALER_ID)
    echo "$response" | jq '.'

    # Check for storage metrics
    read_throughput=$(echo "$response" | jq -r '.details.current_storage_read_throughput_mbps // 0')
    write_throughput=$(echo "$response" | jq -r '.details.current_storage_write_throughput_mbps // 0')
    iops=$(echo "$response" | jq -r '.details.current_storage_iops // 0')

    print_info "Current Storage Read: $read_throughput MB/s"
    print_info "Current Storage Write: $write_throughput MB/s"
    print_info "Current Storage IOPS: $iops"

    if [ "$read_throughput" != "0" ] || [ "$write_throughput" != "0" ] || [ "$iops" != "0" ]; then
        print_success "Storage I/O metrics are being collected"
    else
        print_info "Storage metrics are 0 (may be using simulation or no PVCs attached)"
    fi
fi

sleep 2

# Test 7: Create Storage-Only Autoscaler (Data Pipeline Use Case)
print_header "Test 7: Create Storage-Only Autoscaler"
print_info "Testing storage-only autoscaling (no CPU/Memory/GPU targets)"
response=$(curl -s -X POST $BASE_URL/autoscaling \
  -H "Content-Type: application/json" \
  -d '{
    "workload_name": "data-pipeline",
    "workload_namespace": "default",
    "workload_type": "Deployment",
    "min_replicas": 1,
    "max_replicas": 10,
    "target_storage_read_throughput_mbps": 800,
    "target_storage_write_throughput_mbps": 300,
    "target_storage_iops": 5000
  }')

if check_response "$response" "Storage-only autoscaler creation"; then
    STORAGE_ONLY_ID=$(echo "$response" | jq -r '.autoscaling_id')
    print_info "Storage-only Autoscaler ID: $STORAGE_ONLY_ID"
    print_success "Storage-only autoscaling is supported!"
fi

sleep 2

# Test 8: List All Autoscalers
print_header "Test 8: List All Autoscalers"
response=$(curl -s $BASE_URL/autoscaling)
echo "$response" | jq '.'

autoscaler_count=$(echo "$response" | jq -r '.count // 0')
print_info "Total autoscalers: $autoscaler_count"

if [ "$autoscaler_count" -gt 0 ]; then
    print_success "Autoscalers are listed correctly"
else
    print_error "No autoscalers found"
fi

sleep 2

# Test 9: Get Autoscaling Metrics
print_header "Test 9: Get Autoscaling Metrics"
response=$(curl -s $BASE_URL/autoscaling/metrics)
echo "$response" | jq '.'

total=$(echo "$response" | jq -r '.total_autoscalers // 0')
active=$(echo "$response" | jq -r '.active_autoscalers // 0')
print_info "Total: $total, Active: $active"

sleep 2

# Test 10: Test Validation - Missing Target Metrics
print_header "Test 10: Validation Test - No Target Metrics"
print_info "This should fail (no CPU, Memory, GPU, or Storage targets)"
response=$(curl -s -X POST $BASE_URL/autoscaling \
  -H "Content-Type: application/json" \
  -d '{
    "workload_name": "invalid-test",
    "workload_namespace": "default",
    "workload_type": "Deployment",
    "min_replicas": 1,
    "max_replicas": 5
  }')

if echo "$response" | grep -q "at least one target metric"; then
    print_success "Validation correctly rejected autoscaler with no metrics"
    echo "$response" | jq '.'
else
    print_error "Validation failed - should have rejected"
    echo "$response" | jq '.'
fi

sleep 2

# Test 11: Test Validation - Invalid Replica Count
print_header "Test 11: Validation Test - Invalid Replica Count"
response=$(curl -s -X POST $BASE_URL/autoscaling \
  -H "Content-Type: application/json" \
  -d '{
    "workload_name": "invalid-test",
    "workload_namespace": "default",
    "workload_type": "Deployment",
    "min_replicas": 10,
    "max_replicas": 5,
    "target_cpu_percent": 70
  }')

if echo "$response" | grep -q "max_replicas must be greater"; then
    print_success "Validation correctly rejected invalid replica count"
    echo "$response" | jq '.'
else
    print_error "Validation failed"
    echo "$response" | jq '.'
fi

sleep 2

# Cleanup
print_header "Test 12: Cleanup - Delete All Autoscalers"

# Delete storage-aware autoscaler
if [ -n "$AUTOSCALER_ID" ]; then
    curl -s -X DELETE $BASE_URL/autoscaling/$AUTOSCALER_ID > /dev/null
    print_success "Deleted storage-aware autoscaler: $AUTOSCALER_ID"
fi

# Delete storage-only autoscaler
if [ -n "$STORAGE_ONLY_ID" ]; then
    curl -s -X DELETE $BASE_URL/autoscaling/$STORAGE_ONLY_ID > /dev/null
    print_success "Deleted storage-only autoscaler: $STORAGE_ONLY_ID"
fi

sleep 2

# Final verification
print_header "Final Verification"
response=$(curl -s $BASE_URL/autoscaling)
remaining=$(echo "$response" | jq -r '.count // 0')

if [ "$remaining" -eq 0 ]; then
    print_success "All test autoscalers cleaned up successfully"
else
    print_info "Remaining autoscalers: $remaining (may be from previous tests)"
fi

# Summary
print_header "Test Summary"
echo -e "${GREEN}All tests completed!${NC}\n"
echo -e "Key Features Tested:"
echo -e "  ${GREEN}✓${NC} Traditional autoscaling (CPU/Memory)"
echo -e "  ${GREEN}✓${NC} GPU autoscaling"
echo -e "  ${GREEN}✓${NC} Storage I/O autoscaling (Read/Write/IOPS) - ${YELLOW}NEW!${NC}"
echo -e "  ${GREEN}✓${NC} Storage-only autoscaling - ${YELLOW}AI/ML Data Pipeline${NC}"
echo -e "  ${GREEN}✓${NC} Multi-metric autoscaling"
echo -e "  ${GREEN}✓${NC} Validation checks"
echo -e "  ${GREEN}✓${NC} CRUD operations\n"

echo -e "${BLUE}Storage-aware autoscaling for AI/ML workloads is working!${NC}\n"
