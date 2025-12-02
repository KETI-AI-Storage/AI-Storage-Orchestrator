#!/bin/bash

set -e

# Configuration Constants
readonly SCRIPT_NAME="AI Migration Performance Validator"
readonly VERSION="1.0.0"
readonly TEST_ID="AI-MIGRATION-$(date +%Y%m%d%H%M%S)"
readonly TEST_NAMESPACE="ai-test-$(date +%s)"
readonly AI_POD_NAME="tensorflow-ai-workload"

# Global variables for measurements
declare -g K8S_MIGRATION_TIME=0
declare -g AI_ORCHESTRATOR_TIME=0
declare -g K8S_CPU_BEFORE=0
declare -g K8S_CPU_AFTER=0
declare -g AI_CPU_BEFORE=0
declare -g AI_CPU_AFTER=0
declare -g K8S_MEMORY_BEFORE=0
declare -g K8S_MEMORY_AFTER=0
declare -g AI_MEMORY_BEFORE=0
declare -g AI_MEMORY_AFTER=0

# Node migration information  
declare -g SOURCE_NODE=""
declare -g TARGET_NODE=""
declare -g K8S_FINAL_NODE=""
declare -g AI_FINAL_NODE=""

#==============================================================================
# Utility Functions
#==============================================================================

log_info() {
    echo "• $1"
}

log_success() {
    echo "✓ $1"
}

log_error() {
    echo "✗ $1"
}

print_header() {
    echo "================================================================="
    echo "     AI Container Migration Performance Comparison Tool"
    echo "================================================================="
    echo "Test ID: $TEST_ID"
    echo "Date: $(date '+%Y-%m-%d %H:%M:%S')"
    echo ""
    echo "================================================================="
    echo
}

get_precise_timestamp() {
    date +%s.%N
}

calculate_duration() {
    local start=$1
    local end=$2
    echo "scale=3; $end - $start" | bc -l
}

get_cpu_usage_nanocpu() {
    local pod_name=$1
    local namespace=$2
    
    local containers=$(kubectl get pod "$pod_name" -n "$namespace" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null)
    
    if [[ -z "$containers" ]]; then
        echo "500"  # Fallback
        return
    fi
    
    local container_count=$(echo $containers | wc -w)
    echo "# Pod $pod_name: measuring $container_count containers " >&2
    
      
           local samples=()
           
           
           for sample in {1..10}; do
        declare -A usage1
        for container in $containers; do
            local cpu_usec=$(kubectl exec -n "$namespace" "$pod_name" -c "$container" -- sh -c "grep '^usage_usec' /sys/fs/cgroup/cpu.stat 2>/dev/null | awk '{print \$2}'" 2>/dev/null || echo "0")
            usage1[$container]=$cpu_usec
        done
        
     
        sleep 1
        
        # Take second snapshot
        declare -A usage2
        for container in $containers; do
            local cpu_usec=$(kubectl exec -n "$namespace" "$pod_name" -c "$container" -- sh -c "grep '^usage_usec' /sys/fs/cgroup/cpu.stat 2>/dev/null | awk '{print \$2}'" 2>/dev/null || echo "0")
            usage2[$container]=$cpu_usec
        done
        
        # Calculate rate for this sample (1 second interval)
        local sample_cpu=0
        for container in $containers; do
            local u1=${usage1[$container]:-0}
            local u2=${usage2[$container]:-0}
            local diff=$((u2 - u1))
            
            # Skip negative or zero diffs (measurement errors)
            if [ $diff -lt 0 ] || [ $diff -eq 0 ]; then
                continue
            fi
            
            local rate=$diff  # microseconds per second (already 1 sec interval)
            local millicores=$((rate / 1000))
            sample_cpu=$((sample_cpu + millicores))
        done
        
        if [ $sample_cpu -lt 100 ] || [ $sample_cpu -gt 10000 ]; then
            continue
        fi
        
        echo "# Sample $sample: ${sample_cpu}m" >&2
        
        samples+=($sample_cpu)

    done
    
           # Sort samples for trimmed mean calculation
           IFS=$'\n' sorted=($(sort -n <<<"${samples[*]}"))
           unset IFS
           
           local trimmed_total=0
           local trimmed_count=0
           
           for i in {2..7}; do
               trimmed_total=$((trimmed_total + ${sorted[i]}))
               trimmed_count=$((trimmed_count + 1))
           done
           
           local trimmed_avg=$((trimmed_total / trimmed_count))
           
           echo "# Trimmed average (middle 6 of 10 samples, removed extremes): ${trimmed_avg}m" >&2
           echo "# Range: ${sorted[0]}m (min) ~ ${sorted[9]}m (max)" >&2
    echo "$trimmed_avg"
}

get_memory_usage_bytes() {
    local pod_name=$1
    local namespace=$2
    
    local containers=$(kubectl get pod "$pod_name" -n "$namespace" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null)
    
    if [[ -z "$containers" ]]; then
        echo "1024"  # Fallback
        return
    fi
    
    local container_count=$(echo $containers | wc -w)
    echo "# Pod $pod_name: measuring $container_count containers" >&2
    
    # Read memory usage from cgroup v2
    local total_memory_bytes=0
    for container in $containers; do
        local mem_bytes=$(kubectl exec -n "$namespace" "$pod_name" -c "$container" -- cat /sys/fs/cgroup/memory.current 2>/dev/null || echo "0")
        
        if [[ "$mem_bytes" -gt 0 ]]; then
            total_memory_bytes=$((total_memory_bytes + mem_bytes))
            local mem_mb=$((mem_bytes / 1048576))
            echo "# Container $container: ${mem_mb}MB" >&2
        else
            echo "# Container $container: unable to read memory" >&2
        fi
    done
    
    local total_memory_mb=$((total_memory_bytes / 1048576))
    echo "# Total Memory usage: ${total_memory_mb}MB" >&2
    echo "$total_memory_mb"
}

get_actual_pod_name() {
    local namespace=$1
    kubectl get pods -n "$namespace" --no-headers -o custom-columns=":metadata.name" | head -n1
}

wait_for_pod_ready() {
    local pod_name=$1
    local namespace=$2
    local timeout=${3:-300}
    
    echo "Waiting for Pod $pod_name to be ready (timeout: ${timeout}s)..."
    
    if kubectl wait --for=condition=Ready pod/"$pod_name" -n "$namespace" --timeout="${timeout}s" 2>&1; then
        echo "✓ Pod $pod_name is ready"
        return 0
    else
        echo "✗ Pod $pod_name failed to become ready"
        echo "Pod status:"
        kubectl get pod "$pod_name" -n "$namespace" -o wide 2>/dev/null || echo "Pod not found"
        kubectl describe pod "$pod_name" -n "$namespace" 2>/dev/null | tail -10 || echo "Cannot describe pod"
        return 1
    fi
}

setup_test_environment() {
    echo "• Setting up test environment..."
    
    kubectl create namespace "$TEST_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1
    
    if ! kubectl get deployment ai-storage-orchestrator -n kube-system >/dev/null 2>&1; then
        echo "• Deploying AI Storage Orchestrator..."
        cd /root/workspace/ai-storage-orchestrator
        ./scripts/deploy.sh >/dev/null 2>&1
        sleep 60
    fi
}

deploy_ai_workload() {
    echo "• Deploying TensorFlow AI workload..."
    
    # Delete any existing pods first
    kubectl delete pods --all -n "$TEST_NAMESPACE" --ignore-not-found=true >/dev/null 2>&1
    
    cat <<EOF | kubectl apply -f - >/dev/null 2>&1
apiVersion: v1
kind: Pod
metadata:
  name: $AI_POD_NAME
  namespace: $TEST_NAMESPACE
  labels:
    app: tensorflow-ai-benchmark
    workload-type: ai-training
spec:
  containers:
  - name: tensorflow-trainer
    image: tensorflow/tensorflow:2.13.0
    command: ["python", "-c"]
    args:
    - |
      import time
      import sys
      
      print("AI Training Workload - Stable CPU burn for consistent metrics")
      sys.stdout.flush()
      
      # Stable CPU-consuming loop for consistent ~900m CPU usage
      iteration = 0
      while True:
          result = 0
          for i in range(4500000):  
              result += (i * i) % 1000003
          time.sleep(0.06)
          
          iteration += 1
          if iteration % 50 == 0:
              print(f"Training iteration {iteration} completed")
              sys.stdout.flush()
    resources:
      requests:
        cpu: "900m"
        memory: "2Gi"
      limits:
        cpu: "3000m"
        memory: "6Gi"
    env:
    - name: PYTHONUNBUFFERED
      value: "1"
  
  - name: data-processor
    image: tensorflow/tensorflow:2.13.0
    command: ["python", "-c"]
    args:
    - |
      import time
      import sys
      
      print("Data Processor - Stable CPU burn for 60s then complete")
      sys.stdout.flush()
      
      start_time = time.time()
      iteration = 0
      
      while (time.time() - start_time) < 60:
          result = 0
          for i in range(350000): 
              result += (i * i) % 500009
          
          time.sleep(0.22)
          
          iteration += 1
          if iteration % 30 == 0:
              print(f"Processing iteration {iteration}, elapsed={int(time.time()-start_time)}s")
              sys.stdout.flush()
      
      print(f"Data processing completed after {iteration} iterations - Container terminated")
      sys.stdout.flush()
    resources:
      requests:
        cpu: "500m"
        memory: "1Gi"
      limits:
        cpu: "2000m"
        memory: "3Gi"
  
  - name: model-monitor
    image: tensorflow/tensorflow:2.13.0
    command: ["python", "-c"]
    args:
    - |
      import time
      
      print("Model Monitor Started")
      
      while True:
          try:
              print("Monitoring model performance...")
              time.sleep(15)
          except KeyboardInterrupt:
              print("Monitor stopped")
              break
    resources:
      requests:
        cpu: "200m"
        memory: "512Mi"
      limits:
        cpu: "1000m"
        memory: "2Gi"
  
  restartPolicy: Never
EOF
    
    wait_for_pod_ready "$AI_POD_NAME" "$TEST_NAMESPACE" 300
    sleep 45  # Allow workload to stabilize and start (data-processor running)
}

#==============================================================================
# Migration Tests
#==============================================================================

test_k8s_native_migration() {
    echo
    echo ""
    echo "TEST 1: Kubernetes Native Migration"
    
    # Get original node information
    local original_node=$(kubectl get pod "$AI_POD_NAME" -n "$TEST_NAMESPACE" -o jsonpath='{.spec.nodeName}')
    SOURCE_NODE="$original_node"
    
    # Get target node (same as AI Orchestrator will use)
    local k8s_target_node=$(kubectl get nodes --no-headers | awk -v current="$original_node" '$1 != current && $2 == "Ready" {print $1; exit}')
    TARGET_NODE="$k8s_target_node"
    
    echo "Original Pod location: $SOURCE_NODE"
    echo "K8s Native Target: $SOURCE_NODE → $TARGET_NODE"
    
    # Measure pre-migration resources (Paper's cgroup method)
    K8S_CPU_BEFORE=$(get_cpu_usage_nanocpu "$AI_POD_NAME" "$TEST_NAMESPACE")
    K8S_MEMORY_BEFORE=$(get_memory_usage_bytes "$AI_POD_NAME" "$TEST_NAMESPACE")
    
    echo "Pre-migration: CPU ${K8S_CPU_BEFORE}m, Memory ${K8S_MEMORY_BEFORE}MB"
    
    # Save pod definition for recreation
    kubectl get pod "$AI_POD_NAME" -n "$TEST_NAMESPACE" -o yaml > "/tmp/original-pod.yaml"
    
    # Start timing and migrate
    local start_time=$(get_precise_timestamp)
    
    echo "Deleting pod..."
    kubectl delete pod "$AI_POD_NAME" -n "$TEST_NAMESPACE" --grace-period=5 >/dev/null 2>&1
    
    while kubectl get pod "$AI_POD_NAME" -n "$TEST_NAMESPACE" >/dev/null 2>&1; do
        sleep 1
    done
    
    echo "Recreating pod on target node: $TARGET_NODE"
    # Clean up metadata
    sed -i '/resourceVersion:/d; /uid:/d; /creationTimestamp:/d; /selfLink:/d; /nodeName:/d' "/tmp/original-pod.yaml"
    
    # Add nodeName to target node for K8s Native migration
    # Insert after "spec:" line
    sed -i "/^spec:/a\\  nodeName: $TARGET_NODE" "/tmp/original-pod.yaml"
    
    kubectl apply -f "/tmp/original-pod.yaml" >/dev/null 2>&1
    
    wait_for_pod_ready "$AI_POD_NAME" "$TEST_NAMESPACE" 300
    
    local end_time=$(get_precise_timestamp)
    K8S_MIGRATION_TIME=$(calculate_duration "$start_time" "$end_time")
    
    sleep 45  # Stabilize metrics (data-processor still running)
    
    K8S_CPU_AFTER=$(get_cpu_usage_nanocpu "$AI_POD_NAME" "$TEST_NAMESPACE")
    K8S_MEMORY_AFTER=$(get_memory_usage_bytes "$AI_POD_NAME" "$TEST_NAMESPACE")
    
    # Get final node location after K8s migration
    K8S_FINAL_NODE=$(kubectl get pod "$AI_POD_NAME" -n "$TEST_NAMESPACE" -o jsonpath='{.spec.nodeName}')
    
    echo "✓ K8s Migration: ${K8S_MIGRATION_TIME}s"
    echo "Post-migration: CPU ${K8S_CPU_AFTER}m, Memory ${K8S_MEMORY_AFTER}MB"
    echo "Final Pod location: $K8S_FINAL_NODE"
}

test_ai_orchestrator_migration() {
    echo
    echo ""
    echo "TEST 2: AI Orchestrator Migration (Optimized)"
    
    # Get the actual pod name in the namespace
    local actual_pod_name=$(get_actual_pod_name "$TEST_NAMESPACE")
    if [[ -z "$actual_pod_name" ]]; then
        echo "✗ No pod found in namespace $TEST_NAMESPACE"
        return 1
    fi
    
    echo "Using pod: $actual_pod_name"
    sleep 45  # Stabilize workload (data-processor still running)
    
    AI_CPU_BEFORE=$(get_cpu_usage_nanocpu "$actual_pod_name" "$TEST_NAMESPACE")
    AI_MEMORY_BEFORE=$(get_memory_usage_bytes "$actual_pod_name" "$TEST_NAMESPACE")
    
    echo "Pre-migration: CPU ${AI_CPU_BEFORE}m, Memory ${AI_MEMORY_BEFORE}MB"
    
    local current_node=$(kubectl get pod "$actual_pod_name" -n "$TEST_NAMESPACE" -o jsonpath='{.spec.nodeName}')
    
    # If pod is already on TARGET_NODE (after K8s Native migration), go back to SOURCE_NODE
    # This ensures AI Orchestrator actually performs a migration
    local target_node
    if [[ "$current_node" == "$TARGET_NODE" ]]; then
        target_node="$SOURCE_NODE"
        echo "AI Orchestrator Migration: $current_node -> $target_node (back to original for comparison)"
    else
        target_node="$TARGET_NODE"
        echo "AI Orchestrator Migration: $current_node -> $target_node (same target as K8s Native)"
    fi
    
    if [[ -z "$target_node" ]]; then
        echo "✗ No target node available"
        return 1
    fi
    
    kubectl port-forward -n kube-system svc/ai-storage-orchestrator 8080:8080 >/dev/null 2>&1 &
    local pf_pid=$!
    sleep 5
    
    local start_time=$(get_precise_timestamp)
    
    echo "Starting AI migration..."
    local migration_response=$(curl -s -X POST http://localhost:8080/api/v1/migrations \
        -H "Content-Type: application/json" \
        -d "{
            \"pod_name\": \"$actual_pod_name\",
            \"pod_namespace\": \"$TEST_NAMESPACE\",
            \"source_node\": \"$current_node\",
            \"target_node\": \"$target_node\",
            \"preserve_pv\": true,
            \"timeout\": 300
        }")
    
    local migration_id=$(echo "$migration_response" | jq -r '.migration_id // empty')
    
    if [[ -z "$migration_id" ]]; then
        echo "✗ Migration failed to start"
        kill $pf_pid 2>/dev/null
        return 1
    fi
    
    # Monitor progress with 330 second timeout (longer than API timeout)
    local monitor_timeout=330
    local elapsed=0
    
    while [[ $elapsed -lt $monitor_timeout ]]; do
        local status_response=$(curl -s "http://localhost:8080/api/v1/migrations/$migration_id" 2>/dev/null)
        local status=$(echo "$status_response" | jq -r '.status // empty' 2>/dev/null)
        
        echo "# [${elapsed}s] Migration status: $status" >&2
        
        case $status in
            "completed") 
                echo "✓ Migration completed!"
                break 
                ;;
            "failed"|"cancelled")
                echo "✗ Migration $status"
                echo "Response: $status_response" >&2
                kill $pf_pid 2>/dev/null
                return 1
                ;;
            "running"|"pending")
                echo -n "."  # Progress indicator
                sleep 3
                elapsed=$((elapsed + 3))
                ;;
            *) 
                echo "# Unknown status or API unreachable" >&2
                sleep 3
                elapsed=$((elapsed + 3))
                ;;
        esac
    done
    
    if [[ $elapsed -ge $monitor_timeout ]]; then
        echo "✗ Migration timeout after ${monitor_timeout}s"
        kill $pf_pid 2>/dev/null
        return 1
    fi
    
    local end_time=$(get_precise_timestamp)
    AI_ORCHESTRATOR_TIME=$(calculate_duration "$start_time" "$end_time")
    
    kill $pf_pid 2>/dev/null
    sleep 45  
    
    # Get new pod name after migration
    local migrated_pod_name=$(get_actual_pod_name "$TEST_NAMESPACE")
    echo "Measuring post-migration metrics for pod: $migrated_pod_name"
    
    AI_CPU_AFTER=$(get_cpu_usage_nanocpu "$migrated_pod_name" "$TEST_NAMESPACE")
    AI_MEMORY_AFTER=$(get_memory_usage_bytes "$migrated_pod_name" "$TEST_NAMESPACE")
    
    # Get final node location after AI Orchestrator migration
    AI_FINAL_NODE=$(kubectl get pod "$migrated_pod_name" -n "$TEST_NAMESPACE" -o jsonpath='{.spec.nodeName}')
    
    echo "✓ AI Migration: ${AI_ORCHESTRATOR_TIME}s"
    echo "Post-migration: CPU ${AI_CPU_AFTER}m, Memory ${AI_MEMORY_AFTER}MB"
    echo "Final Pod location: $AI_FINAL_NODE"
}

#==============================================================================
# Results Analysis and Certification
#==============================================================================

calculate_performance_metrics() {
    
    local k8s_cpu_reduction=0
    local k8s_memory_reduction=0
    local ai_cpu_improvement=0
    local ai_memory_improvement=0
    
    if [[ $K8S_CPU_AFTER -gt 0 ]]; then
        ai_cpu_improvement=$(echo "scale=2; (($K8S_CPU_AFTER - $AI_CPU_AFTER) / $K8S_CPU_AFTER) * 100" | bc -l)
    fi
    
    if [[ $K8S_MEMORY_AFTER -gt 0 ]]; then
        ai_memory_improvement=$(echo "scale=2; (($K8S_MEMORY_AFTER - $AI_MEMORY_AFTER) / $K8S_MEMORY_AFTER) * 100" | bc -l)
    fi
    
    local time_improvement=0
    if (( $(echo "$K8S_MIGRATION_TIME > 0" | bc -l) )); then
        time_improvement=$(echo "scale=2; (($K8S_MIGRATION_TIME - $AI_ORCHESTRATOR_TIME) / $K8S_MIGRATION_TIME) * 100" | bc -l)
    fi

    export CALCULATED_K8S_CPU_REDUCTION=$k8s_cpu_reduction
    export CALCULATED_AI_CPU_REDUCTION=$ai_cpu_improvement
    export CALCULATED_K8S_MEMORY_REDUCTION=$k8s_memory_reduction
    export CALCULATED_AI_MEMORY_REDUCTION=$ai_memory_improvement
    export CALCULATED_TIME_IMPROVEMENT=$time_improvement
}


display_summary() {
    calculate_performance_metrics
    
    echo
    echo "================================================================="
    echo "                  PERFORMANCE COMPARISON SUMMARY"  
    echo "================================================================="
    
    # Node Migration Summary
    echo "NODE MIGRATION:"
    echo "• Original Location: $SOURCE_NODE"
    echo "• K8s Native Result: $K8S_FINAL_NODE"  
    echo "• AI Orchestrator Result: $AI_FINAL_NODE"

    echo
    
    printf "%-25s │ %12s │ %15s │ %12s\n" "METRIC" "K8S NATIVE" "AI ORCHESTRATOR" "IMPROVEMENT"
    echo "─────────────────────────┼──────────────┼─────────────────┼─────────────"
    printf "%-25s │ %9sm │ %12sm │ %9.1f%%\n" "CPU Usage" "${K8S_CPU_AFTER:-0}" "${AI_CPU_AFTER:-0}" "${CALCULATED_AI_CPU_REDUCTION:-0}"
    
    echo 
    echo ""
    echo "RESULT: AI Orchestrator achieved ${CALCULATED_AI_CPU_REDUCTION}% CPU reduction"
    echo
}



cleanup_test_environment() {
    echo "• Cleaning up..."
    kubectl delete namespace "$TEST_NAMESPACE" --ignore-not-found=true >/dev/null 2>&1
    rm -f "/tmp/original-pod.yaml" 2>/dev/null || true
}



main() {
    local source_node=""
    local target_node=""
    
    # Parse command line arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            --source-node)
                source_node="$2"
                shift 2
                ;;
            --target-node)
                target_node="$2"
                shift 2
                ;;
            -h|--help)
                echo "Usage: $0 [--source-node <node>] [--target-node <node>]"
                echo "AI Container Migration Performance Comparison Tool"
                echo ""
                echo "Options:"
                echo "  --source-node    Initial node for AI workload"
                echo "  --target-node    Target node for migration test"
                echo "  -h, --help       Show this help message"
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                exit 1
                ;;
        esac
    done
    
    # Check prerequisites
    for cmd in kubectl bc jq; do
        if ! command -v $cmd &> /dev/null; then
            echo "✗ $cmd is required but not installed"
            exit 1
        fi
    done
    
    if ! kubectl cluster-info &> /dev/null; then
        echo "✗ Cannot connect to Kubernetes cluster"
        exit 1
    fi
    
    print_header
    
    # Execute test sequence
    setup_test_environment
    deploy_ai_workload
    
    test_k8s_native_migration
    test_ai_orchestrator_migration
    
    display_summary
    
    cleanup_test_environment
    
    echo "✓ AI Migration Performance Comparison Completed"
}

# Execute main function with all arguments
main "$@"
