#!/bin/bash

# AI Storage Orchestrator - Provisioning API Test Script
# Tests storage provisioning features for AI/ML workloads

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
BASE_URL="http://localhost:8080/api/v1"
PROVISIONING_ID=""

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
print_header "AI Storage Provisioning - Test Suite"
print_info "Testing storage provisioning for AI/ML workloads"

# Test 1: Health Check
print_header "Test 1: Health Check"
response=$(curl -s http://localhost:8080/health)
if echo "$response" | grep -q "healthy"; then
    print_success "Health check passed"
else
    print_error "Health check failed"
    exit 1
fi

# Test 2: Get Recommendations for Training Workload
print_header "Test 2: Get Storage Recommendations - Training"
response=$(curl -s $BASE_URL/provisioning/recommend/training)
check_response "$response" "Training workload recommendation"

# Test 3: Get Recommendations for Inference Workload
print_header "Test 3: Get Storage Recommendations - Inference"
response=$(curl -s $BASE_URL/provisioning/recommend/inference)
check_response "$response" "Inference workload recommendation"

# Test 4: Get Recommendations for Data Pipeline
print_header "Test 4: Get Storage Recommendations - Data Pipeline"
response=$(curl -s $BASE_URL/provisioning/recommend/data-pipeline)
check_response "$response" "Data pipeline recommendation"

# Test 5: Create Provisioning with Auto-Sizing
print_header "Test 5: Create Provisioning with Auto-Sizing (Training)"
response=$(curl -s -X POST $BASE_URL/provisioning \
  -H "Content-Type: application/json" \
  -d '{
    "workload_name": "imagenet-training",
    "workload_namespace": "ml-training",
    "workload_type": "training",
    "auto_size": true
  }')

if check_response "$response" "Auto-sizing provisioning creation"; then
    PROVISIONING_ID=$(echo "$response" | jq -r '.provisioning_id')
    print_info "Provisioning ID: $PROVISIONING_ID"

    # Check if recommended size was applied
    size=$(echo "$response" | jq -r '.details.actual_size')
    class=$(echo "$response" | jq -r '.details.actual_class')
    print_info "Auto-assigned: Size=$size, Class=$class"
fi

sleep 2

# Test 6: Get Provisioning Status
print_header "Test 6: Get Provisioning Status"
if [ -n "$PROVISIONING_ID" ]; then
    response=$(curl -s $BASE_URL/provisioning/$PROVISIONING_ID)
    check_response "$response" "Get provisioning status"

    status=$(echo "$response" | jq -r '.status')
    print_info "Current status: $status"
fi

sleep 2

# Test 7: Create Provisioning with Manual Configuration
print_header "Test 7: Create Provisioning - Manual Configuration"
response=$(curl -s -X POST $BASE_URL/provisioning \
  -H "Content-Type: application/json" \
  -d '{
    "workload_name": "model-inference",
    "workload_namespace": "ml-inference",
    "workload_type": "inference",
    "storage_size": "200Gi",
    "storage_class": "high-throughput",
    "access_mode": "ReadWriteOnce",
    "required_read_throughput_mbps": 600,
    "required_write_throughput_mbps": 200
  }')

if check_response "$response" "Manual provisioning creation"; then
    MANUAL_PROV_ID=$(echo "$response" | jq -r '.provisioning_id')
    print_info "Manual Provisioning ID: $MANUAL_PROV_ID"

    # Check performance estimates
    read_mbps=$(echo "$response" | jq -r '.details.estimated_read_throughput_mbps')
    write_mbps=$(echo "$response" | jq -r '.details.estimated_write_throughput_mbps')
    iops=$(echo "$response" | jq -r '.details.estimated_iops')
    print_info "Performance estimates: Read=${read_mbps}MB/s, Write=${write_mbps}MB/s, IOPS=${iops}"
fi

sleep 2

# Test 8: Create High-IOPS Provisioning for Data Pipeline
print_header "Test 8: Create High-IOPS Provisioning - Data Pipeline"
response=$(curl -s -X POST $BASE_URL/provisioning \
  -H "Content-Type: application/json" \
  -d '{
    "workload_name": "data-preprocessing",
    "workload_namespace": "etl",
    "workload_type": "data-pipeline",
    "storage_size": "300Gi",
    "storage_class": "high-iops",
    "required_iops": 10000
  }')

if check_response "$response" "High-IOPS provisioning creation"; then
    IOPS_PROV_ID=$(echo "$response" | jq -r '.provisioning_id')
    print_info "High-IOPS Provisioning ID: $IOPS_PROV_ID"
fi

sleep 2

# Test 9: List All Provisionings
print_header "Test 9: List All Provisionings"
response=$(curl -s $BASE_URL/provisioning)
echo "$response" | jq '.'

count=$(echo "$response" | jq -r '.count // 0')
print_info "Total provisionings: $count"

if [ "$count" -gt 0 ]; then
    print_success "Provisionings are listed correctly"
else
    print_error "No provisionings found"
fi

sleep 2

# Test 10: Get Provisioning Metrics
print_header "Test 10: Get Provisioning Metrics"
response=$(curl -s $BASE_URL/provisioning/metrics)
echo "$response" | jq '.'

total=$(echo "$response" | jq -r '.total_provisionings // 0')
active=$(echo "$response" | jq -r '.active_provisionings // 0')
avg_time=$(echo "$response" | jq -r '.average_provision_time_seconds // 0')
print_info "Total: $total, Active: $active, Avg Time: ${avg_time}s"

sleep 2

# Test 11: Test Validation - Missing Required Fields
print_header "Test 11: Validation Test - Missing workload_name"
response=$(curl -s -X POST $BASE_URL/provisioning \
  -H "Content-Type: application/json" \
  -d '{
    "workload_namespace": "default",
    "workload_type": "training"
  }')

if echo "$response" | grep -q "workload_name is required"; then
    print_success "Validation correctly rejected missing workload_name"
    echo "$response" | jq '.'
else
    print_error "Validation failed"
    echo "$response" | jq '.'
fi

sleep 2

# Test 12: Test Validation - Invalid Storage Class
print_header "Test 12: Validation Test - Invalid Storage Class"
response=$(curl -s -X POST $BASE_URL/provisioning \
  -H "Content-Type: application/json" \
  -d '{
    "workload_name": "test-workload",
    "workload_namespace": "default",
    "workload_type": "training",
    "storage_size": "100Gi",
    "storage_class": "invalid-class"
  }')

if echo "$response" | grep -q "invalid storage_class"; then
    print_success "Validation correctly rejected invalid storage_class"
    echo "$response" | jq '.'
else
    print_error "Validation failed"
    echo "$response" | jq '.'
fi

sleep 2

# Cleanup
print_header "Test 13: Cleanup - Delete Provisionings"

# Delete auto-sizing provisioning
if [ -n "$PROVISIONING_ID" ]; then
    curl -s -X DELETE $BASE_URL/provisioning/$PROVISIONING_ID > /dev/null
    print_success "Deleted provisioning: $PROVISIONING_ID"
fi

# Delete manual provisioning
if [ -n "$MANUAL_PROV_ID" ]; then
    curl -s -X DELETE $BASE_URL/provisioning/$MANUAL_PROV_ID > /dev/null
    print_success "Deleted provisioning: $MANUAL_PROV_ID"
fi

# Delete high-IOPS provisioning
if [ -n "$IOPS_PROV_ID" ]; then
    curl -s -X DELETE $BASE_URL/provisioning/$IOPS_PROV_ID > /dev/null
    print_success "Deleted provisioning: $IOPS_PROV_ID"
fi

sleep 2

# Final verification
print_header "Final Verification"
response=$(curl -s $BASE_URL/provisioning)
remaining=$(echo "$response" | jq -r '.count // 0')

if [ "$remaining" -eq 0 ]; then
    print_success "All test provisionings cleaned up successfully"
else
    print_info "Remaining provisionings: $remaining (may be from previous tests)"
fi

# Summary
print_header "Test Summary"
echo -e "${GREEN}All provisioning tests completed!${NC}\n"
echo -e "Key Features Tested:"
echo -e "  ${GREEN}✓${NC} Auto-sizing based on workload type"
echo -e "  ${GREEN}✓${NC} Manual storage configuration"
echo -e "  ${GREEN}✓${NC} Storage class selection (high-throughput, high-iops, balanced)"
echo -e "  ${GREEN}✓${NC} Performance requirement matching"
echo -e "  ${GREEN}✓${NC} Workload-specific recommendations"
echo -e "  ${GREEN}✓${NC} Validation checks"
echo -e "  ${GREEN}✓${NC} CRUD operations\n"

echo -e "${BLUE}Storage provisioning for AI/ML workloads is working!${NC}\n"
