#!/usr/bin/env bash
#
# AI Orchestrator vs K8s Native CPU 비교 스크립트 (인자 없음, 노드·간섭 강도 고정 규칙).
# baseline 은 간섭 노드에서, AI 워크로드는 저간섭 노드에서 동일 trainer KPI 로 비교한다.
#
# Author: 미정
# Created: 2026-04-13

set -euo pipefail

#------------------------------------------------------------------------------
# 버전·네임스페이스·오브젝트 이름
#------------------------------------------------------------------------------
readonly SCRIPT_VERSION="2.0.0"
readonly TEST_NS="ai-migtest-$(date +%s)"
readonly BASELINE_POD="migtest-baseline"
readonly ORCHESTRATED_POD="migtest-ai"
readonly TRAINER_CONTAINER="tensorflow-trainer"
readonly MONITOR_CONTAINER="model-monitor"
readonly TRAINER_IMAGE="tensorflow/tensorflow:2.13.0"
readonly INTERFERENCE_DEPLOY="migtest-cpu-interference"

# trainer 루프 강도 (튜닝 시 마지막 순위로만 조정)
readonly TRAINER_INNER_ITERS=4500000
readonly TRAINER_LOOP_SLEEP_SEC="0.06"

#------------------------------------------------------------------------------
# 시간 기준 (초) — 환경변수로 덮어쓰기 가능 (튜닝 가이드 3순위)
#------------------------------------------------------------------------------
readonly INTERFERENCE_STABILIZE_SEC="${MIGTEST_INTERFERENCE_STABILIZE_SEC:-30}"
readonly WORKLOAD_INIT_STABILIZE_SEC="${MIGTEST_WORKLOAD_INIT_STABILIZE_SEC:-45}"
readonly POST_MIGRATION_STABILIZE_SEC="${MIGTEST_POST_MIGRATION_STABILIZE_SEC:-75}"

#------------------------------------------------------------------------------
# CPU 샘플링 (trainer 컨테이너, usage_usec 차분)
#------------------------------------------------------------------------------
readonly CPU_SAMPLE_INTERVAL_SEC=1
readonly CPU_SAMPLE_COUNT=10
readonly TRIMMED_START_IDX=2
readonly TRIMMED_END_IDX=7

#------------------------------------------------------------------------------
# Orchestrator API
#------------------------------------------------------------------------------
readonly ORCHESTRATOR_NS="kube-system"
readonly ORCHESTRATOR_SVC="ai-storage-orchestrator"
readonly ORCHESTRATOR_PORT="8080"
readonly ORCHESTRATOR_LOCAL_PORT="${ORCHESTRATOR_LOCAL_PORT:-18080}"
readonly MIGRATION_API_TIMEOUT_SEC=300
readonly MIGRATION_POLL_INTERVAL_SEC=3
readonly MIGRATION_POLL_TIMEOUT_SEC=330

#------------------------------------------------------------------------------
# 간섭 강도 (고정: medium 과 동일. 필요 시 MIGTEST_INTERFERENCE_* 만 덮어쓰기)
#------------------------------------------------------------------------------
declare -g IF_REPLICAS=2
declare -g IF_REQ_CPU="450m"
declare -g IF_LIM_CPU="900m"

#------------------------------------------------------------------------------
# 전역 (cleanup)
#------------------------------------------------------------------------------
declare -g CLEANUP_DONE=0
declare -g PF_PID=""
declare -g BASELINE_JSON_TMP=""
declare -g SOURCE_NODE=""
declare -g BUSY_NODE=""
declare -g CALM_NODE=""

#------------------------------------------------------------------------------
log_info() { echo "• $*" >&2; }
log_ok() { echo "✓ $*" >&2; }
log_err() { echo "✗ $*" >&2; }

print_header() {
    echo "================================================================="
    echo "  AI Orchestrator CPU reduction experiment (trainer KPI)"
    echo "================================================================="
    echo "Script: ai_migration_compare_test.sh v${SCRIPT_VERSION}"
    echo "Namespace: ${TEST_NS}"
    echo "Interference: ${IF_REPLICAS} pods × req ${IF_REQ_CPU} (고정 프로파일)"
    echo "Date: $(date '+%Y-%m-%d %H:%M:%S')"
    echo "================================================================="
    echo
}

#------------------------------------------------------------------------------
apply_interference_profile() {
    IF_REPLICAS=2
    IF_REQ_CPU="450m"
    IF_LIM_CPU="900m"
    if [[ -n "${MIGTEST_INTERFERENCE_REPLICAS:-}" ]]; then
        IF_REPLICAS="${MIGTEST_INTERFERENCE_REPLICAS}"
    fi
    if [[ -n "${MIGTEST_INTERFERENCE_REQ_CPU:-}" ]]; then
        IF_REQ_CPU="${MIGTEST_INTERFERENCE_REQ_CPU}"
    fi
    if [[ -n "${MIGTEST_INTERFERENCE_LIM_CPU:-}" ]]; then
        IF_LIM_CPU="${MIGTEST_INTERFERENCE_LIM_CPU}"
    fi
}

#------------------------------------------------------------------------------
check_prereqs() {
    local c
    for c in kubectl jq bc curl; do
        if ! command -v "$c" &>/dev/null; then
            log_err "필수 명령 없음: $c"
            exit 1
        fi
    done
    if ! kubectl cluster-info &>/dev/null; then
        log_err "Kubernetes 클러스터에 연결할 수 없습니다."
        exit 1
    fi
    if ! kubectl get deployment "${ORCHESTRATOR_SVC}" -n "${ORCHESTRATOR_NS}" &>/dev/null; then
        log_err "Deployment ${ORCHESTRATOR_NS}/${ORCHESTRATOR_SVC} 가 없습니다."
        exit 1
    fi
    if kubectl get hpa ai-storage-orchestrator-hpa -n "${ORCHESTRATOR_NS}" &>/dev/null; then
        local hpa_max
        hpa_max=$(kubectl get hpa ai-storage-orchestrator-hpa -n "${ORCHESTRATOR_NS}" -o jsonpath='{.spec.maxReplicas}' 2>/dev/null || echo "?")
        log_info "HPA ai-storage-orchestrator-hpa 존재 (maxReplicas=${hpa_max}). Pod 가 흔들리면:"
        log_info "  kubectl delete hpa ai-storage-orchestrator-hpa -n ${ORCHESTRATOR_NS}"
    fi
}

#------------------------------------------------------------------------------
# 노드별 Pod 수 (전 네임스페이스)
#------------------------------------------------------------------------------
_count_pods_on_node() {
    local nd="$1"
    kubectl get pods -A --field-selector "spec.nodeName=${nd}" --no-headers 2>/dev/null | wc -l
}

# Ready 노드 목록 (이름 정렬)
_list_ready_nodes() {
    kubectl get nodes --no-headers 2>/dev/null | awk '$2 == "Ready" {print $1}' | sort
}

# 자동: Pod 많은 노드 = busy, 적은 노드 = calm, 나머지 하나 = source (3노드 필수)
resolve_nodes_auto() {
    local -a nodes
    mapfile -t nodes < <(_list_ready_nodes)
    local n="${#nodes[@]}"
    log_info "[auto] Ready 노드 ${n}개: ${nodes[*]}"
    if (( n < 3 )); then
        log_err "[auto] source/busy/calm 을 모두 다르게 쓰려면 Ready 노드가 최소 3개 필요합니다."
        exit 1
    fi

    local -a scored=()
    local nd cnt
    for nd in "${nodes[@]}"; do
        cnt=$(_count_pods_on_node "$nd")
        scored+=("${cnt} ${nd}")
    done
    IFS=$'\n' readarray -t sorted < <(printf '%s\n' "${scored[@]}" | sort -n -k1,1 -k2,2)
    unset IFS

    local calm_line busy_line
    calm_line="${sorted[0]}"
    busy_line="${sorted[$((n - 1))]}"
    CALM_NODE=$(echo "$calm_line" | awk '{print $2}')
    BUSY_NODE=$(echo "$busy_line" | awk '{print $2}')

    SOURCE_NODE=""
    local line nn
    for line in "${sorted[@]}"; do
        nn=$(echo "$line" | awk '{print $2}')
        if [[ "$nn" != "$CALM_NODE" && "$nn" != "$BUSY_NODE" ]]; then
            SOURCE_NODE="$nn"
            break
        fi
    done
    if [[ -z "$SOURCE_NODE" ]]; then
        log_err "[auto] SOURCE 후보를 찾지 못했습니다 (노드 구성 확인)."
        exit 1
    fi

    log_info "[auto] BUSY_NODE=${BUSY_NODE} (상대적으로 Pod 많음)"
    log_info "[auto] CALM_NODE=${CALM_NODE} (상대적으로 Pod 적음)"
    log_info "[auto] SOURCE_NODE=${SOURCE_NODE} (두 워크로드 시작 위치)"
}

validate_nodes() {
    if [[ "$SOURCE_NODE" == "$BUSY_NODE" ]]; then
        log_err "SOURCE 와 BUSY 는 달라야 합니다."
        exit 1
    fi
    if [[ "$SOURCE_NODE" == "$CALM_NODE" ]]; then
        log_err "SOURCE 와 CALM 은 달라야 합니다 (Orchestrator: source_node ≠ target_node)."
        exit 1
    fi
    if [[ "$BUSY_NODE" == "$CALM_NODE" ]]; then
        log_err "BUSY 와 CALM 은 달라야 합니다."
        exit 1
    fi
    local nd st
    for nd in "$SOURCE_NODE" "$BUSY_NODE" "$CALM_NODE"; do
        st=$(kubectl get node "$nd" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
        if [[ "$st" != "True" ]]; then
            log_err "노드가 Ready 가 아님: ${nd}"
            exit 1
        fi
    done
}

#------------------------------------------------------------------------------
cleanup() {
    if [[ "$CLEANUP_DONE" == "1" ]]; then
        return 0
    fi
    CLEANUP_DONE=1
    log_info "Cleaning up (trap)..."
    if [[ -n "${PF_PID:-}" ]] && kill -0 "$PF_PID" 2>/dev/null; then
        kill "$PF_PID" 2>/dev/null || true
        wait "$PF_PID" 2>/dev/null || true
    fi
    kubectl delete namespace "$TEST_NS" --ignore-not-found=true --wait=false &>/dev/null || true
    rm -f "${BASELINE_JSON_TMP:-}" 2>/dev/null || true
    log_ok "Cleanup requested."
}

#------------------------------------------------------------------------------
create_namespace() {
    kubectl create namespace "$TEST_NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    log_ok "Namespace ${TEST_NS}"
}

# busy 노드에만 간섭 Deployment (단일 stress 컨테이너/Pod, 종료 없음)
deploy_interference() {
    log_info "간섭 워크로드: ${BUSY_NODE} 에 Deployment ${INTERFERENCE_DEPLOY} (${IF_REPLICAS} replicas, req ${IF_REQ_CPU}, lim ${IF_LIM_CPU})"
    cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${INTERFERENCE_DEPLOY}
  namespace: ${TEST_NS}
  labels:
    migtest/component: cpu-interference
spec:
  replicas: ${IF_REPLICAS}
  selector:
    matchLabels:
      migtest/component: cpu-interference
  template:
    metadata:
      labels:
        migtest/component: cpu-interference
    spec:
      nodeName: ${BUSY_NODE}
      containers:
      - name: stress
        image: busybox:1.36
        command: ["sh", "-c", "while true; do :; done"]
        resources:
          requests:
            cpu: "${IF_REQ_CPU}"
          limits:
            cpu: "${IF_LIM_CPU}"
EOF
    kubectl rollout status deployment/"${INTERFERENCE_DEPLOY}" -n "$TEST_NS" --timeout=180s
    log_ok "간섭 Deployment Ready"
    log_info "간섭 안정화 ${INTERFERENCE_STABILIZE_SEC}s 대기..."
    sleep "$INTERFERENCE_STABILIZE_SEC"
}

workload_pod_yaml() {
    local name="$1"
    local role_label="$2"
    local node="$3"
    cat <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${name}
  namespace: ${TEST_NS}
  labels:
    migtest/role: "${role_label}"
    app: migtest-ai-workload
spec:
  nodeName: ${node}
  containers:
  - name: ${TRAINER_CONTAINER}
    image: ${TRAINER_IMAGE}
    command: ["python", "-c"]
    args:
    - |
      import time, sys
      print("migtest trainer (stable CPU loop)", flush=True)
      it = 0
      inner = ${TRAINER_INNER_ITERS}
      sleep_s = ${TRAINER_LOOP_SLEEP_SEC}
      while True:
          x = 0
          for i in range(inner):
              x += (i * i) % 1000003
          time.sleep(sleep_s)
          it += 1
          if it % 50 == 0:
              print("trainer iter", it, flush=True)
    resources:
      requests:
        cpu: "800m"
        memory: "2Gi"
      limits:
        cpu: "2500m"
        memory: "6Gi"
    env:
    - name: PYTHONUNBUFFERED
      value: "1"
  - name: ${MONITOR_CONTAINER}
    image: ${TRAINER_IMAGE}
    command: ["python", "-c"]
    args:
    - |
      import time
      while True:
          print("monitor", flush=True)
          time.sleep(15)
    resources:
      requests:
        cpu: "100m"
        memory: "512Mi"
      limits:
        cpu: "500m"
        memory: "2Gi"
  restartPolicy: Always
EOF
}

deploy_workload_pair() {
    log_info "동일 구조 Pod 2개를 SOURCE ${SOURCE_NODE} 에 배치..."
    workload_pod_yaml "$BASELINE_POD" "baseline" "$SOURCE_NODE" | kubectl apply -f - >/dev/null
    workload_pod_yaml "$ORCHESTRATED_POD" "orchestrated" "$SOURCE_NODE" | kubectl apply -f - >/dev/null
    kubectl wait --for=condition=Ready "pod/${BASELINE_POD}" -n "$TEST_NS" --timeout=300s
    kubectl wait --for=condition=Ready "pod/${ORCHESTRATED_POD}" -n "$TEST_NS" --timeout=300s
    log_ok "두 Pod Ready"
    log_info "워크로드 초기 안정화 ${WORKLOAD_INIT_STABILIZE_SEC}s..."
    sleep "$WORKLOAD_INIT_STABILIZE_SEC"
}

#------------------------------------------------------------------------------
_parse_quantity_to_millicores() {
    local q="$1"
    q="${q//$'\r'/}"
    q="${q// /}"
    [[ -z "$q" || "$q" == "null" ]] && echo "0" && return
    if [[ "$q" =~ ^([0-9]+)m$ ]]; then
        echo "${BASH_REMATCH[1]}"
        return
    fi
    if [[ "$q" =~ ^([0-9]+)n$ ]]; then
        echo $((${BASH_REMATCH[1]} / 1000000))
        return
    fi
    if [[ "$q" =~ ^([0-9]+)$ ]]; then
        echo "${BASH_REMATCH[1]}"
        return
    fi
    echo "0"
}

_top_container_cpu_millicores_once() {
    local pod="$1"
    local ns="$2"
    local container="$3"
    local raw
    raw=$(kubectl top pod "$pod" -n "$ns" --containers -o json 2>/dev/null \
        | jq -r --arg c "$container" '.containers[] | select(.name==$c) | .usage.cpu' 2>/dev/null | head -1)
    _parse_quantity_to_millicores "$raw"
}

_top_pod_total_millicores_once() {
    local pod="$1"
    local ns="$2"
    local sum=0
    local q
    while IFS= read -r q; do
        local m
        m=$(_parse_quantity_to_millicores "$q")
        sum=$((sum + m))
    done < <(kubectl top pod "$pod" -n "$ns" --containers -o json 2>/dev/null | jq -r '.containers[].usage.cpu' 2>/dev/null)
    echo "$sum"
}

_cpu_counter_read() {
    local pod="$1"
    local ns="$2"
    local container="$3"
    kubectl exec -n "$ns" "$pod" -c "$container" -- sh -c '
      if grep -q "^usage_usec" /sys/fs/cgroup/cpu.stat 2>/dev/null; then
        printf "usec %s" "$(grep "^usage_usec" /sys/fs/cgroup/cpu.stat | awk "{print \$2}")"
      elif test -r /sys/fs/cgroup/cpuacct/cpuacct.usage; then
        printf "nano %s" "$(cat /sys/fs/cgroup/cpuacct/cpuacct.usage)"
      elif test -r /sys/fs/cgroup/cpu,cpuacct/cpuacct.usage; then
        printf "nano %s" "$(cat /sys/fs/cgroup/cpu,cpuacct/cpuacct.usage)"
      else
        printf "none 0"
      fi
    ' 2>/dev/null | tr -d "\r" || echo "none 0"
}

_sample_container_cpu_millicores() {
    local pod="$1"
    local ns="$2"
    local container="$3"
    local kind u1 kind2 u2 diff mc

    read -r kind u1 < <(_cpu_counter_read "$pod" "$ns" "$container")
    [[ -z "${u1// /}" || "$kind" == "none" ]] && u1=0

    sleep "$CPU_SAMPLE_INTERVAL_SEC"

    read -r kind2 u2 < <(_cpu_counter_read "$pod" "$ns" "$container")
    [[ -z "${u2// /}" || "$kind2" == "none" ]] && u2=0

    if [[ "$kind" == "usec" && "$kind2" == "usec" ]]; then
        diff=$((u2 - u1))
        if [[ "$diff" -gt 0 ]]; then
            echo $((diff / 1000))
            return
        fi
    elif [[ "$kind" == "nano" && "$kind2" == "nano" ]]; then
        diff=$((u2 - u1))
        if [[ "$diff" -gt 0 ]]; then
            echo $((diff / 1000000))
            return
        fi
    fi

    mc=$(_top_container_cpu_millicores_once "$pod" "$ns" "$container")
    echo "${mc:-0}"
}

_sample_pod_total_cpu_millicores() {
    local pod="$1"
    local ns="$2"
    local names
    names=$(kubectl get pod "$pod" -n "$ns" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null || true)
    local total=0
    local c
    for c in $names; do
        local m
        m=$(_sample_container_cpu_millicores "$pod" "$ns" "$c")
        total=$((total + m))
    done
    echo "$total"
}

_measure_trimmed_trainer() {
    local pod="$1"
    local ns="$2"
    local -a samples=()
    local i m
    for ((i = 1; i <= CPU_SAMPLE_COUNT; i++)); do
        m=$(_sample_container_cpu_millicores "$pod" "$ns" "$TRAINER_CONTAINER")
        echo "# [${pod}] trainer sample ${i}: ${m}m" >&2
        samples+=("$m")
    done
    local sorted
    IFS=$'\n' sorted=($(sort -n <<<"${samples[*]}")); unset IFS
    local sum=0 cnt=0
    for ((i = TRIMMED_START_IDX; i <= TRIMMED_END_IDX; i++)); do
        sum=$((sum + sorted[i]))
        cnt=$((cnt + 1))
    done
    echo $((sum / cnt))
}

_measure_trimmed_pod_total() {
    local pod="$1"
    local ns="$2"
    local -a samples=()
    local i m
    for ((i = 1; i <= CPU_SAMPLE_COUNT; i++)); do
        m=$(_sample_pod_total_cpu_millicores "$pod" "$ns")
        echo "# [${pod}] pod-total sample ${i}: ${m}m" >&2
        samples+=("$m")
    done
    local sorted
    IFS=$'\n' sorted=($(sort -n <<<"${samples[*]}")); unset IFS
    local sum=0 cnt=0
    for ((i = TRIMMED_START_IDX; i <= TRIMMED_END_IDX; i++)); do
        sum=$((sum + sorted[i]))
        cnt=$((cnt + 1))
    done
    echo $((sum / cnt))
}

# cgroup v2: 1초 창에서 throttled_usec 증가량(스케줄러 스로틀). busy 노드에서 보통 더 큼.
_read_trainer_throttled_usec() {
    local pod="$1"
    local ns="$2"
    local v
    v=$(kubectl exec -n "$ns" "$pod" -c "$TRAINER_CONTAINER" -- sh -c \
        "grep '^throttled_usec' /sys/fs/cgroup/cpu.stat 2>/dev/null | awk '{print \$2}'" 2>/dev/null | tr -d "\r" || true)
    [[ -z "${v// /}" ]] && v=0
    echo "$v"
}

_sample_trainer_throttled_usec_delta() {
    local pod="$1"
    local ns="$2"
    local t1 t2 d
    t1=$(_read_trainer_throttled_usec "$pod" "$ns")
    sleep "$CPU_SAMPLE_INTERVAL_SEC"
    t2=$(_read_trainer_throttled_usec "$pod" "$ns")
    d=$((t2 - t1))
    [[ "$d" -lt 0 ]] && d=0
    echo "$d"
}

_measure_trimmed_throttle() {
    local pod="$1"
    local ns="$2"
    local -a samples=()
    local i m
    for ((i = 1; i <= CPU_SAMPLE_COUNT; i++)); do
        m=$(_sample_trainer_throttled_usec_delta "$pod" "$ns")
        echo "# [${pod}] throttled_usec Δ sample ${i}: ${m} us/s" >&2
        samples+=("$m")
    done
    local sorted
    IFS=$'\n' sorted=($(sort -n <<<"${samples[*]}")); unset IFS
    local sum=0 cnt=0
    for ((i = TRIMMED_START_IDX; i <= TRIMMED_END_IDX; i++)); do
        sum=$((sum + sorted[i]))
        cnt=$((cnt + 1))
    done
    echo $((sum / cnt))
}

# 마이그레이션 완료 후 안정화 + trainer·Pod 합계·스로틀(trimmed mean)
measure_after_stabilize() {
    local pod="$1"
    local ns="$2"
    local label="$3"
    log_info "${label}: 마이그레이션 후 안정화 ${POST_MIGRATION_STABILIZE_SEC}s → trainer KPI + throttled_usec (${CPU_SAMPLE_COUNT}회 샘플)"
    sleep "$POST_MIGRATION_STABILIZE_SEC"
    local tr tot thr
    tr=$(_measure_trimmed_trainer "$pod" "$ns")
    tot=$(_measure_trimmed_pod_total "$pod" "$ns")
    thr=$(_measure_trimmed_throttle "$pod" "$ns")
    echo "$tr $tot $thr"
}

#------------------------------------------------------------------------------
k8s_native_migrate_to_busy() {
    log_info "Baseline: Pod JSON export → nodeName=${BUSY_NODE} → delete → apply"
    BASELINE_JSON_TMP=$(mktemp)
    kubectl get pod "$BASELINE_POD" -n "$TEST_NS" -o json >"$BASELINE_JSON_TMP"
    jq --arg nn "$BUSY_NODE" \
        'del(.metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp,
              .metadata.selfLink, .metadata.generation, .metadata.managedFields, .status)
         | .spec.nodeName = $nn' "$BASELINE_JSON_TMP" >"${BASELINE_JSON_TMP}.out"
    mv "${BASELINE_JSON_TMP}.out" "$BASELINE_JSON_TMP"

    kubectl delete pod "$BASELINE_POD" -n "$TEST_NS" --grace-period=10 --wait=true
    kubectl apply -f "$BASELINE_JSON_TMP"
    kubectl wait --for=condition=Ready "pod/${BASELINE_POD}" -n "$TEST_NS" --timeout=300s
    local fn
    fn=$(kubectl get pod "$BASELINE_POD" -n "$TEST_NS" -o jsonpath='{.spec.nodeName}')
    if [[ "$fn" != "$BUSY_NODE" ]]; then
        log_err "baseline 최종 노드 불일치: 기대 ${BUSY_NODE}, 실제 ${fn}"
        exit 1
    fi
    log_ok "Baseline Pod 가 busy 노드에 있음: ${fn}"
}

start_port_forward() {
    if command -v fuser &>/dev/null; then
        fuser -k "${ORCHESTRATOR_LOCAL_PORT}/tcp" 2>/dev/null || true
        sleep 1
    fi
    kubectl port-forward -n "${ORCHESTRATOR_NS}" "svc/${ORCHESTRATOR_SVC}" \
        "${ORCHESTRATOR_LOCAL_PORT}:${ORCHESTRATOR_PORT}" >/dev/null 2>&1 &
    PF_PID=$!
    sleep 5
    if ! kill -0 "$PF_PID" 2>/dev/null; then
        log_err "port-forward 실패"
        exit 1
    fi
}

stop_port_forward() {
    if [[ -n "${PF_PID:-}" ]] && kill -0 "$PF_PID" 2>/dev/null; then
        kill "$PF_PID" 2>/dev/null || true
        wait "$PF_PID" 2>/dev/null || true
    fi
    PF_PID=""
}

orchestrator_migrate_to_calm() {
    start_port_forward
    local current
    current=$(kubectl get pod "$ORCHESTRATED_POD" -n "$TEST_NS" -o jsonpath='{.spec.nodeName}')
    if [[ "$current" != "$SOURCE_NODE" ]]; then
        log_err "AI Pod 가 SOURCE 에 없음: ${current}"
        stop_port_forward
        exit 1
    fi
    log_info "POST /api/v1/migrations (${ORCHESTRATED_POD}: ${current} → ${CALM_NODE})"
    local resp
    if ! resp=$(curl -sfS -X POST "http://127.0.0.1:${ORCHESTRATOR_LOCAL_PORT}/api/v1/migrations" \
        -H "Content-Type: application/json" \
        -d "{
            \"pod_name\": \"${ORCHESTRATED_POD}\",
            \"pod_namespace\": \"${TEST_NS}\",
            \"source_node\": \"${current}\",
            \"target_node\": \"${CALM_NODE}\",
            \"preserve_pv\": true,
            \"timeout\": ${MIGRATION_API_TIMEOUT_SEC}
        }"); then
        log_err "POST /api/v1/migrations 실패 (port-forward·네트워크 확인)"
        stop_port_forward
        exit 1
    fi

    local mid
    mid=$(echo "$resp" | jq -r '.migration_id // empty')
    if [[ -z "$mid" ]]; then
        log_err "migration_id 없음. 응답: ${resp}"
        stop_port_forward
        exit 1
    fi
    log_ok "migration_id=${mid}"

    local elapsed=0
    while [[ "$elapsed" -lt "$MIGRATION_POLL_TIMEOUT_SEC" ]]; do
        local st raw
        raw=$(curl -sS "http://127.0.0.1:${ORCHESTRATOR_LOCAL_PORT}/api/v1/migrations/${mid}" || echo "{}")
        st=$(echo "$raw" | jq -r '.status // empty')
        echo "# [${elapsed}s] migration status: ${st}" >&2
        case "$st" in
            completed)
                log_ok "Orchestrator migration completed"
                break
                ;;
            failed|cancelled)
                log_err "migration ${st}. body: ${raw}"
                stop_port_forward
                exit 1
                ;;
            pending|running|"")
                sleep "$MIGRATION_POLL_INTERVAL_SEC"
                elapsed=$((elapsed + MIGRATION_POLL_INTERVAL_SEC))
                ;;
            *)
                log_err "알 수 없는 status=${st} body=${raw}"
                stop_port_forward
                exit 1
                ;;
        esac
    done
    if [[ "$elapsed" -ge "$MIGRATION_POLL_TIMEOUT_SEC" ]]; then
        log_err "migration 타임아웃 (${MIGRATION_POLL_TIMEOUT_SEC}s)"
        stop_port_forward
        exit 1
    fi
    stop_port_forward
}

resolve_orchestrated_pod_name() {
    local name
    name=$(kubectl get pods -n "$TEST_NS" -l 'migtest/role=orchestrated' \
        -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | grep -E 'migrated-' | head -1 || true)
    if [[ -z "$name" ]]; then
        name=$(kubectl get pods -n "$TEST_NS" -l 'migtest/role=orchestrated' \
            -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    fi
    if [[ -z "$name" ]]; then
        log_err "orchestrated 역할 Pod 를 찾을 수 없습니다."
        exit 1
    fi
    echo "$name"
}

verify_orchestrated_on_calm() {
    local pod="$1"
    local fn
    fn=$(kubectl get pod "$pod" -n "$TEST_NS" -o jsonpath='{.spec.nodeName}')
    if [[ "$fn" != "$CALM_NODE" ]]; then
        log_err "AI 최종 노드 불일치: 기대 ${CALM_NODE}, 실제 ${fn}"
        exit 1
    fi
    local cnt
    cnt=$(kubectl get pod "$pod" -n "$TEST_NS" -o jsonpath='{.spec.containers[*].name}' | wc -w)
    if [[ "${cnt// /}" != "2" ]]; then
        log_err "컨테이너 수가 2가 아님: ${cnt}"
        exit 1
    fi
    log_ok "AI Pod ${pod} on calm ${fn}"
}

#------------------------------------------------------------------------------
display_summary() {
    local baseline_trainer="$1"
    local ai_trainer="$2"
    local baseline_total="$3"
    local ai_total="$4"
    local baseline_thr="$5"
    local ai_thr="$6"

    local pct="0.00"
    if [[ "${baseline_trainer}" =~ ^[0-9]+$ && "${ai_trainer}" =~ ^[0-9]+$ && "${baseline_trainer}" -gt 0 ]]; then
        pct=$(echo "scale=2; 100 * (${baseline_trainer} - ${ai_trainer}) / ${baseline_trainer}" | bc -l)
    else
        log_err "trainer 측정값 비정상: baseline=${baseline_trainer}, ai=${ai_trainer}"
    fi
    local pct_fmt
    pct_fmt=$(printf "%.2f" "${pct}")

    local thr_pct_fmt="0.00"
    if [[ "${baseline_thr}" =~ ^[0-9]+$ && "${ai_thr}" =~ ^[0-9]+$ && "${baseline_thr}" -gt 0 ]]; then
        thr_pct_fmt=$(printf "%.2f" "$(echo "scale=2; 100 * (${baseline_thr} - ${ai_thr}) / ${baseline_thr}" | bc -l)")
    fi

    echo
    echo "================================================================="
    echo "                         SUMMARY"
    echo "================================================================="
    echo "• source node:     ${SOURCE_NODE}"
    echo "• busy node:       ${BUSY_NODE}"
    echo "• calm node:       ${CALM_NODE}"
    echo "• baseline node:   ${BUSY_NODE}"
    echo "• AI node:         ${CALM_NODE}"
    echo
    echo "KPI (${TRAINER_CONTAINER}, usage_usec → millicores, trimmed mean):"
    printf "  %-34s %8sm\n" "baseline CPU (trainer, busy)" "${baseline_trainer}"
    printf "  %-34s %8sm\n" "AI CPU (trainer, calm)" "${ai_trainer}"
    printf "  %-34s %8s%%\n" "usage reduction (참고)" "${pct_fmt}"
    echo
    echo "KPI (${TRAINER_CONTAINER}, cgroup cpu.stat throttled_usec — 1초당 증가량 trimmed mean, us/s):"
    printf "  %-34s %8s\n" "baseline throttled_usec rate (busy)" "${baseline_thr}"
    printf "  %-34s %8s\n" "AI throttled_usec rate (calm)" "${ai_thr}"
    if [[ "${baseline_thr}" =~ ^[0-9]+$ && "${baseline_thr}" -gt 0 ]]; then
        printf "  %-34s %8s%%\n" "throttling reduction (vs busy)" "${thr_pct_fmt}"
    else
        echo "  (스로틀 비교 불가: baseline throttled 가 0 이거나 cgroup v2 미노출)"
    fi
    echo
    echo "보조 (Pod 전체 합산 millicores):"
    printf "  %-34s %8sm\n" "baseline pod total" "${baseline_total}"
    printf "  %-34s %8sm\n" "AI pod total" "${ai_total}"
    echo "----------------------------------------------------------------"
    echo "해석: 동일 CPU-bound 루프는 usage(m) 차이가 거의 없을 수 있음(정상)."
    echo "      혼잼 노드로의 배치는 throttled_usec(스케줄 스로틀)에서 더 잘 드러나며,"
    echo "      calm 으로 옮긴 쪽이 스로틀 증가율이 낮으면 스케줄링 측면 이득으로 볼 수 있음."
    echo "================================================================="

    if [[ "${baseline_thr}" =~ ^[0-9]+$ && "${baseline_thr}" -gt 0 ]]; then
        echo "RESULT: calm 노드에서 trainer 스로틀링(throttled_usec 증가율)이 busy 대비 ${thr_pct_fmt}% 낮게 측정됨 (usage millicore 절감 ${pct_fmt}%)"
    else
        echo "RESULT: trainer usage millicore 절감 ${pct_fmt}% (스로틀 지표 미사용 또는 0)"
    fi
    echo "================================================================="
}

if [[ $# -gt 0 ]]; then
    log_err "이 스크립트는 인자를 받지 않습니다. 실행: $0"
    exit 1
fi

apply_interference_profile

main() {
    check_prereqs
    print_header
    trap cleanup EXIT INT TERM

    resolve_nodes_auto
    validate_nodes

    create_namespace
    deploy_interference
    deploy_workload_pair

    k8s_native_migrate_to_busy
    local bt btot bthr
    read -r bt btot bthr < <(measure_after_stabilize "$BASELINE_POD" "$TEST_NS" "Baseline (busy)")
    if [[ ! "$bt" =~ ^[0-9]+$ || ! "$btot" =~ ^[0-9]+$ || ! "$bthr" =~ ^[0-9]+$ ]]; then
        log_err "baseline 측정 파싱 실패: bt='${bt}' btot='${btot}' bthr='${bthr}'"
        exit 1
    fi

    orchestrator_migrate_to_calm
    local ai_pod
    ai_pod=$(resolve_orchestrated_pod_name)
    verify_orchestrated_on_calm "$ai_pod"
    local at atot athr
    read -r at atot athr < <(measure_after_stabilize "$ai_pod" "$TEST_NS" "AI (calm)")
    if [[ ! "$at" =~ ^[0-9]+$ || ! "$atot" =~ ^[0-9]+$ || ! "$athr" =~ ^[0-9]+$ ]]; then
        log_err "AI 측정 파싱 실패: at='${at}' atot='${atot}' athr='${athr}'"
        exit 1
    fi

    display_summary "$bt" "$at" "$btot" "$atot" "$bthr" "$athr"
    log_ok "실험 완료"
}

main
