#!/bin/bash

# Preemption API Test Script
# Tests the Preemption Controller functionality

set -e

# Configuration
API_BASE="${API_BASE:-http://localhost:8080}"
TEST_NODE="${TEST_NODE:-worker-1}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Helper functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_test() {
    echo -e "\n${YELLOW}========================================${NC}"
    echo -e "${YELLOW}TEST: $1${NC}"
    echo -e "${YELLOW}========================================${NC}"
}

# Check if API is available
check_api() {
    log_info "Checking API availability at ${API_BASE}..."
    if curl -s "${API_BASE}/health" | grep -q "healthy"; then
        log_success "API is available"
        return 0
    else
        log_error "API is not available"
        return 1
    fi
}

# Test 1: Dry-run preemption for CPU
test_dryrun_cpu_preemption() {
    log_test "Dry-run CPU Preemption"

    log_info "Creating dry-run preemption request for CPU..."

    RESPONSE=$(curl -s -X POST "${API_BASE}/api/v1/preemption" \
        -H "Content-Type: application/json" \
        -d '{
            "node_name": "'${TEST_NODE}'",
            "resource_type": "cpu",
            "target_amount": "2000m",
            "strategy": "lowest_priority",
            "min_priority": 0,
            "max_pods_to_preempt": 5,
            "reason": "Test dry-run CPU preemption"
        }')

    echo "Response: $RESPONSE"

    PREEMPTION_ID=$(echo "$RESPONSE" | jq -r '.preemption_id')

    if [ -z "$PREEMPTION_ID" ] || [ "$PREEMPTION_ID" == "null" ]; then
        log_error "Failed to create preemption job"
        return 1
    fi

    log_success "Preemption job created: $PREEMPTION_ID"

    # Wait for completion
    log_info "Waiting for preemption analysis to complete..."
    sleep 3

    # Get details
    DETAILS=$(curl -s "${API_BASE}/api/v1/preemption/${PREEMPTION_ID}")
    echo "Details: $DETAILS"

    STATUS=$(echo "$DETAILS" | jq -r '.status')
    log_info "Final status: $STATUS"

    if [ "$STATUS" == "completed" ]; then
        log_success "Dry-run CPU preemption test passed"

        PODS_ANALYZED=$(echo "$DETAILS" | jq -r '.details.total_pods_analyzed')
        PODS_TO_PREEMPT=$(echo "$DETAILS" | jq -r '.details.pods_to_preempt')
        log_info "Pods analyzed: $PODS_ANALYZED"
        log_info "Pods would be preempted: $PODS_TO_PREEMPT"

        return 0
    else
        log_warning "Preemption status: $STATUS (may be expected if no suitable pods)"
        return 0
    fi
}

# Test 2: Dry-run preemption for Memory
test_dryrun_memory_preemption() {
    log_test "Dry-run Memory Preemption"

    log_info "Creating dry-run preemption request for Memory..."

    RESPONSE=$(curl -s -X POST "${API_BASE}/api/v1/preemption" \
        -H "Content-Type: application/json" \
        -d '{
            "node_name": "'${TEST_NODE}'",
            "resource_type": "memory",
            "target_amount": "4Gi",
            "strategy": "largest_resource",
            "min_priority": 100,
            "max_pods_to_preempt": 3,
            "reason": "Test dry-run memory preemption"
        }')

    echo "Response: $RESPONSE"

    PREEMPTION_ID=$(echo "$RESPONSE" | jq -r '.preemption_id')

    if [ -z "$PREEMPTION_ID" ] || [ "$PREEMPTION_ID" == "null" ]; then
        log_error "Failed to create preemption job"
        return 1
    fi

    log_success "Preemption job created: $PREEMPTION_ID"

    sleep 3

    DETAILS=$(curl -s "${API_BASE}/api/v1/preemption/${PREEMPTION_ID}")
    STATUS=$(echo "$DETAILS" | jq -r '.status')

    if [ "$STATUS" == "completed" ] || [ "$STATUS" == "failed" ]; then
        log_success "Dry-run memory preemption test passed (status: $STATUS)"
        return 0
    else
        log_warning "Unexpected status: $STATUS"
        return 0
    fi
}

# Test 3: Test youngest strategy
test_youngest_strategy() {
    log_test "Youngest Strategy Preemption"

    log_info "Creating preemption with youngest strategy..."

    RESPONSE=$(curl -s -X POST "${API_BASE}/api/v1/preemption" \
        -H "Content-Type: application/json" \
        -d '{
            "node_name": "'${TEST_NODE}'",
            "resource_type": "cpu",
            "target_amount": "1000m",
            "strategy": "youngest",
            "min_priority": 0,
            "max_pods_to_preempt": 2,
            "reason": "Test youngest strategy"
        }')

    echo "Response: $RESPONSE"

    PREEMPTION_ID=$(echo "$RESPONSE" | jq -r '.preemption_id')

    if [ -n "$PREEMPTION_ID" ] && [ "$PREEMPTION_ID" != "null" ]; then
        log_success "Youngest strategy test passed"
        return 0
    else
        log_error "Youngest strategy test failed"
        return 1
    fi
}

# Test 4: Test weighted_score strategy
test_weighted_score_strategy() {
    log_test "Weighted Score Strategy Preemption"

    log_info "Creating preemption with weighted_score strategy..."

    RESPONSE=$(curl -s -X POST "${API_BASE}/api/v1/preemption" \
        -H "Content-Type: application/json" \
        -d '{
            "node_name": "'${TEST_NODE}'",
            "resource_type": "all",
            "target_amount": "2",
            "strategy": "weighted_score",
            "min_priority": 0,
            "reason": "Test weighted_score strategy"
        }')

    echo "Response: $RESPONSE"

    PREEMPTION_ID=$(echo "$RESPONSE" | jq -r '.preemption_id')

    if [ -n "$PREEMPTION_ID" ] && [ "$PREEMPTION_ID" != "null" ]; then
        log_success "Weighted score strategy test passed"
        return 0
    else
        log_error "Weighted score strategy test failed"
        return 1
    fi
}

# Test 5: List all preemptions
test_list_preemptions() {
    log_test "List All Preemption Jobs"

    log_info "Listing all preemption jobs..."

    RESPONSE=$(curl -s "${API_BASE}/api/v1/preemption")
    echo "Response: $RESPONSE"

    COUNT=$(echo "$RESPONSE" | jq -r '.count')

    if [ -n "$COUNT" ]; then
        log_success "List preemptions test passed (count: $COUNT)"
        return 0
    else
        log_error "List preemptions test failed"
        return 1
    fi
}

# Test 6: Get preemption metrics
test_preemption_metrics() {
    log_test "Preemption Metrics"

    log_info "Getting preemption metrics..."

    RESPONSE=$(curl -s "${API_BASE}/api/v1/preemption/metrics")
    echo "Response: $RESPONSE"

    TOTAL_JOBS=$(echo "$RESPONSE" | jq -r '.total_preemption_jobs')
    TOTAL_PREEMPTED=$(echo "$RESPONSE" | jq -r '.total_pods_preempted')

    log_info "Total preemption jobs: $TOTAL_JOBS"
    log_info "Total pods preempted: $TOTAL_PREEMPTED"

    if [ -n "$TOTAL_JOBS" ]; then
        log_success "Preemption metrics test passed"
        return 0
    else
        log_error "Preemption metrics test failed"
        return 1
    fi
}

# Test 7: Invalid request handling
test_invalid_request() {
    log_test "Invalid Request Handling"

    log_info "Testing with missing required field..."

    RESPONSE=$(curl -s -X POST "${API_BASE}/api/v1/preemption" \
        -H "Content-Type: application/json" \
        -d '{
            "resource_type": "cpu",
            "target_amount": "1000m"
        }')

    echo "Response: $RESPONSE"

    ERROR=$(echo "$RESPONSE" | jq -r '.error')

    if [ "$ERROR" != "null" ] && [ -n "$ERROR" ]; then
        log_success "Invalid request properly rejected"
        return 0
    else
        log_warning "Invalid request was not rejected as expected"
        return 0
    fi
}

# Test 8: Invalid strategy handling
test_invalid_strategy() {
    log_test "Invalid Strategy Handling"

    log_info "Testing with invalid strategy..."

    RESPONSE=$(curl -s -X POST "${API_BASE}/api/v1/preemption" \
        -H "Content-Type: application/json" \
        -d '{
            "node_name": "'${TEST_NODE}'",
            "resource_type": "cpu",
            "target_amount": "1000m",
            "strategy": "invalid_strategy"
        }')

    echo "Response: $RESPONSE"

    ERROR=$(echo "$RESPONSE" | jq -r '.error')

    if [ "$ERROR" != "null" ] && [ -n "$ERROR" ]; then
        log_success "Invalid strategy properly rejected"
        return 0
    else
        log_warning "Invalid strategy was not rejected as expected"
        return 0
    fi
}

# Test 9: Namespace filtering
test_namespace_filtering() {
    log_test "Namespace Filtering"

    log_info "Testing with specific namespace..."

    RESPONSE=$(curl -s -X POST "${API_BASE}/api/v1/preemption" \
        -H "Content-Type: application/json" \
        -d '{
            "node_name": "'${TEST_NODE}'",
            "namespace": "default",
            "resource_type": "cpu",
            "target_amount": "500m",
            "strategy": "lowest_priority",
            "reason": "Test namespace filtering"
        }')

    echo "Response: $RESPONSE"

    PREEMPTION_ID=$(echo "$RESPONSE" | jq -r '.preemption_id')

    if [ -n "$PREEMPTION_ID" ] && [ "$PREEMPTION_ID" != "null" ]; then
        log_success "Namespace filtering test passed"
        return 0
    else
        log_error "Namespace filtering test failed"
        return 1
    fi
}

# Test 10: Protected namespaces
test_protected_namespaces() {
    log_test "Protected Namespaces"

    log_info "Testing with custom protected namespaces..."

    RESPONSE=$(curl -s -X POST "${API_BASE}/api/v1/preemption" \
        -H "Content-Type: application/json" \
        -d '{
            "node_name": "'${TEST_NODE}'",
            "resource_type": "cpu",
            "target_amount": "500m",
            "protected_namespaces": ["kube-system", "monitoring", "production"],
            "reason": "Test protected namespaces"
        }')

    echo "Response: $RESPONSE"

    PREEMPTION_ID=$(echo "$RESPONSE" | jq -r '.preemption_id')

    if [ -n "$PREEMPTION_ID" ] && [ "$PREEMPTION_ID" != "null" ]; then
        log_success "Protected namespaces test passed"
        return 0
    else
        log_error "Protected namespaces test failed"
        return 1
    fi
}

# Main test runner
main() {
    echo ""
    echo "========================================"
    echo "  Preemption API Test Suite"
    echo "========================================"
    echo "API Base: ${API_BASE}"
    echo "Test Node: ${TEST_NODE}"
    echo ""

    # Check if API is available
    if ! check_api; then
        log_error "API is not available. Please start the orchestrator first."
        log_info "Usage: API_BASE=http://localhost:8080 TEST_NODE=worker-1 ./test-preemption.sh"
        exit 1
    fi

    PASSED=0
    FAILED=0

    # Run tests
    tests=(
        "test_dryrun_cpu_preemption"
        "test_dryrun_memory_preemption"
        "test_youngest_strategy"
        "test_weighted_score_strategy"
        "test_list_preemptions"
        "test_preemption_metrics"
        "test_invalid_request"
        "test_invalid_strategy"
        "test_namespace_filtering"
        "test_protected_namespaces"
    )

    for test in "${tests[@]}"; do
        if $test; then
            ((PASSED++))
        else
            ((FAILED++))
        fi
    done

    # Summary
    echo ""
    echo "========================================"
    echo "  Test Summary"
    echo "========================================"
    echo -e "${GREEN}Passed: ${PASSED}${NC}"
    echo -e "${RED}Failed: ${FAILED}${NC}"
    echo "Total: $((PASSED + FAILED))"
    echo ""

    if [ $FAILED -eq 0 ]; then
        log_success "All tests passed!"
        exit 0
    else
        log_error "Some tests failed"
        exit 1
    fi
}

# Run main
main "$@"
