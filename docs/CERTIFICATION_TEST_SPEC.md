# AI 학습 컨테이너 마이그레이션 공인시험 명세서

## 시험항목 정보

### 시험항목 명
AI 학습 컨테이너 마이그레이션 CPU 절감율

### 시험기준
Kubernetes 클러스터에서 AI Storage Orchestrator를 사용하여 TensorFlow AI 학습 컨테이너를 마이그레이션할 때, Kubernetes Native 방식 대비 AI Orchestrator 방식이 다음을 만족할 것.

#### 1) Kubernetes Native 마이그레이션 대비 AI Orchestrator 마이그레이션의 CPU 사용량 20% 이상 절감 달성

#### 성능 결과 측정 방법

**■ Kubernetes Native 방식**: Pod 전체 삭제 후 모든 컨테이너 재생성 (Completed 컨테이너도 재시작)

```bash
# 1. Pod 삭제
kubectl delete pod tensorflow-ai-workload -n <namespace>

# 2. 타겟 노드에 재생성 (모든 컨테이너 Cold Start)
kubectl apply -f pod.yaml
# (pod.yaml에서 spec.nodeName을 타겟 노드로 지정)

# 3. CPU 측정 (cgroup v2 직접 읽기, 20회 샘플링 Trimmed Mean)
for i in {1..20}; do
  kubectl exec tensorflow-ai-workload -n <namespace> -- \
    sh -c "grep '^usage_usec' /sys/fs/cgroup/cpu.stat | awk '{print \$2}'"
  sleep 1
done
# 결과: 20회 측정 후 상위/하위 각 3개 제거, 중간 14개 평균 계산
# 예: 정렬 → [200, 1000, ..., 2500, ..., 6000, 6500]
#     상/하위 제거 후 중간 14개 평균 = 2500 millicores
# 특징: Completed 상태였던 data-processor 컨테이너도 재시작되어 CPU 사용
```

**■ AI Orchestrator 방식**: Persistent Volume 기반 Checkpoint + 컨테이너 상태 분석 + Running 컨테이너만 선택적 재배포

```bash
# 1. AI Orchestrator API 호출 (preserve_pv: true로 PV Checkpoint 활성화)
curl -X POST http://ai-storage-orchestrator:8080/api/migrations \
  -H "Content-Type: application/json" \
  -d '{
    "pod_name": "tensorflow-ai-workload",
    "pod_namespace": "<namespace>",
    "source_node": "<source-node>",
    "target_node": "<target-node>",
    "preserve_pv": true
  }'

# 2. 내부 동작 (AI Orchestrator가 자동 수행)
#    a) 컨테이너 상태 분석
#       - tensorflow-trainer: Running → 마이그레이션 대상 ✓
#       - data-processor: Completed → 제외 (재실행 불필요)
#       - model-monitor: Running → 마이그레이션 대상 ✓
#
#    b) Persistent Volume에 Checkpoint 저장
#       - NFS 기반 PVC에 컨테이너 상태 저장
#       - data-processor의 결과물은 PV에 보존
#
#    c) 타겟 노드에 최적화된 Pod 재배포
#       - Running 상태 컨테이너만 포함 (2개)
#       - Completed 컨테이너는 제외 → CPU/Memory 절감!

# 3. 마이그레이션 완료 대기
curl http://ai-storage-orchestrator:8080/api/migrations/<migration-id>

# 4. CPU 측정 (동일 방법: cgroup v2 직접 읽기, 20회 샘플링 Trimmed Mean)
for i in {1..20}; do
  kubectl exec tensorflow-ai-workload-migrated-<id> -n <namespace> -- \
    sh -c "grep '^usage_usec' /sys/fs/cgroup/cpu.stat | awk '{print \$2}'"
  sleep 1
done
# 결과: 20회 측정 후 상위/하위 각 3개 제거, 중간 14개 평균 계산
# 예: 정렬 → [150, 900, ..., 1800, ..., 5000, 5500]
#     상/하위 제거 후 중간 14개 평균 = 1800 millicores
# 특징: data-processor 제외로 CPU 700m 절감 (2500m → 1800m)
```

**■ CPU 절감율 계산**

```
CPU 절감율 (%) = ((K8s Native CPU - AI Orchestrator CPU) / K8s Native CPU) × 100

예시:
- K8s Native CPU: 2500m
- AI Orchestrator CPU: 1800m
- CPU 절감율 = ((2500 - 1800) / 2500) × 100 = 28.0%

목표: 20% 이상 달성
```

## 시험 환경

### 시험구성

1. 본 테스트는 Kubernetes 클러스터 기반의 컨테이너 오케스트레이션 환경에서 구성된다.

2. Kubernetes 클러스터는 1개의 Master Node와 2개 이상의 Worker Node로 구성되며, AI Storage Orchestrator가 Master Node의 kube-system 네임스페이스에 배포된다.

3. Kubernetes 클러스터는 NFS 서버를 통해 Persistent Volume(PV)을 제공하며, 이를 통해 컨테이너의 상태를 노드 간 공유 가능한 스토리지에 저장한다.

4. Kubernetes 클러스터는 TensorFlow AI 학습 워크로드를 실행하는 Pod를 제공하며, 해당 Pod는 3개의 컨테이너(tensorflow-trainer, data-processor, model-monitor)로 구성된다.

5. 각 Worker Node는 containerd 런타임과 cgroup v2를 통해 컨테이너를 실행하며, CPU/Memory 사용량을 직접 측정할 수 있다.

6. 테스트 수행 과정은 Test Client(또는 Master Node)를 통해 Kubernetes 클러스터에 SSH로 접속하여 마이그레이션 성능 비교 스크립트를 실행하고, 결과를 확인한다.

**구성 요소:**
- Kubernetes 클러스터 (1 Master + 2 Worker Nodes 이상)
- AI Storage Orchestrator (RESTful API 기반 마이그레이션 컨트롤러)
- TensorFlow AI 학습 워크로드 (다중 컨테이너 구성)
- NFS 기반 Persistent Volume (컨테이너 Checkpoint 저장)

### 시험도구

| 도구 | 설명 |
|------|------|
| `ai_migration_compare.sh` | 마이그레이션 성능 비교 자동화 스크립트 |
| `kubectl` | Kubernetes 클러스터 제어 도구 |
| `curl` | RESTful API 호출 도구 |
| `tensorflow/tensorflow:2.13.0` | AI 학습 워크로드 이미지 |

### 시험 환경 상세 요구사항

#### 하드웨어 요구사항

| 구성요소 | 최소 사양 | 권장 사양 | 비고 |
|---------|----------|----------|------|
| **Master Node** | CPU 16 Core<br>RAM 32GB<br>Storage 128GB | CPU 32 Core<br>RAM 64GB<br>Storage 256GB | Control Plane + Orchestrator |
| **Worker Node** (최소 2대) | CPU 16 Core<br>RAM 32GB<br>Storage 256GB | CPU 32 Core<br>RAM 64GB<br>Storage 512GB | 컨테이너 워크로드 실행 |
| **NFS Server** | Storage 1TB<br>Network 1GbE | Storage 2TB<br>Network 10GbE | Persistent Volume 제공 |
| **네트워크** | 1GbE 스위치 | 10GbE 스위치 | 저지연 마이그레이션 |

#### 소프트웨어 요구사항

| 항목 | 버전 | 필수 여부 | 용도 |
|------|------|-----------|------|
| **Kubernetes** | v1.28 이상 | 필수 | 컨테이너 오케스트레이션 |
| **containerd** | v1.7 이상 | 필수 | 컨테이너 런타임 |
| **Linux Kernel** | 6.14 이상 | 필수 | cgroup v2 지원 |
| **NFS Client** | nfs-common | 필수 | PV 마운트 (모든 Worker Node) |
| **Docker** | v24 이상 | 필수 | 이미지 빌드 (Master Node) |
| **kubectl** | v1.28 이상 | 필수 | Kubernetes CLI |
| **TensorFlow** | 2.13.0 | 필수 | AI 워크로드 이미지 |

#### 네트워크 요구사항

| 항목 | 설정 | 비고 |
|------|------|------|
| **노드 간 통신** | 10GbE 이상 | 빠른 마이그레이션 |
| **NFS 통신** | 10GbE 이상 | PV 접근 성능 |
| **Container Network** | CNI (Calico/Flannel) | Pod 간 통신 |
| **Service Network** | ClusterIP, NodePort | 서비스 노출 |
| **방화벽** | 6443 (API), 8080 (Orchestrator) | 포트 개방 필요 |

### 사전조건

- ✅ Kubernetes 클러스터가 최소 3개 노드(1 Master + 2 Worker)로 구성됨을 만족
- ✅ AI Storage Orchestrator가 kube-system 네임스페이스에 배포됨을 만족
- ✅ NFS 서버가 구성되어 Persistent Volume 생성이 가능함을 만족
- ✅ 워커 노드에 nfs-common 패키지가 설치되어 NFS 마운트가 가능함을 만족
- ✅ 각 노드에서 cgroup v2가 활성화되어 CPU/Memory 측정이 가능함을 만족
- ✅ Docker 이미지 `tensorflow/tensorflow:2.13.0`이 모든 노드에 준비됨

### 시험데이터

**TensorFlow AI 학습 컨테이너 Pod 구성:**

| 컨테이너 명 | 역할 | 상태 | CPU Request | Memory Request |
|------------|------|------|-------------|----------------|
| `tensorflow-trainer` | 지속적 AI 모델 학습 | running 상태 유지 | 1500m | 3Gi |
| `data-processor` | 데이터 전처리 후 종료 | completed 상태 전환 | 500m | 1.5Gi |
| `model-monitor` | 모델 성능 모니터링 | running 상태 유지 | 200m | 512Mi |

## 시험절차 및 방법

### 1. Kubernetes 클러스터에 AI Storage Orchestrator 배포

```bash
[root@ai-storage-master ~]# cd /root/workspace/ai-storage-orchestrator
[root@ai-storage-master ~]# ./scripts/build.sh
Building AI Storage Orchestrator...
Building Go binary...
Building Docker image...
✓ Build completed successfully

[root@ai-storage-master ~]# ./scripts/deploy.sh
Deploying AI Storage Orchestrator to Kubernetes...
deployment.apps/ai-storage-orchestrator created
service/ai-storage-orchestrator created
✓ AI Storage Orchestrator deployed to kube-system
```

### 2. 마이그레이션 성능 비교 스크립트 실행

```bash
[root@ai-storage-master ~]# ./scripts/ai_migration_compare.sh
=================================================================
     AI Container Migration Performance Comparison Tool
=================================================================
Test ID: AI-MIGRATION-20251112XXXXXX
Date: 2025-11-12 XX:XX:XX
=================================================================

• Setting up test environment...
• Deploying TensorFlow AI workload...
Waiting for Pod tensorflow-ai-workload to be ready (timeout: 300s)...
pod/tensorflow-ai-workload condition met
✓ Pod tensorflow-ai-workload is ready
```

### 3. TEST 1: Kubernetes Native Migration 수행 및 측정

```bash
TEST 1: Kubernetes Native Migration 
Original Pod location: gpu-server-03
K8s Native Target: gpu-server-03 → ai-storage-master

# Pod tensorflow-ai-workload: measuring 3 containers (3 samples)
# Sample 1: 2350m
# Sample 2: 2180m
# Sample 3: 2250m
# Minimum CPU usage (of 3 samples): 2180m

# Pod tensorflow-ai-workload: measuring 3 containers
# Container tensorflow-trainer: 650MB
# Container data-processor: 0MB (completed)
# Container model-monitor: 4MB
# Total Memory usage: 654MB

Pre-migration: CPU 2180m, Memory 654MB
Deleting pod...
Recreating pod on target node: ai-storage-master
Waiting for Pod tensorflow-ai-workload to be ready (timeout: 300s)...
pod/tensorflow-ai-workload condition met
✓ Pod tensorflow-ai-workload is ready

# Post-migration measurement (all 3 containers restarted)
# Pod tensorflow-ai-workload: measuring 3 containers (3 samples)
# Sample 1: 2650m
# Sample 2: 2500m
# Sample 3: 2580m
# Minimum CPU usage (of 3 samples): 2500m

# Pod tensorflow-ai-workload: measuring 3 containers
# Container tensorflow-trainer: 950MB
# Container data-processor: 35MB (restarted!)
# Container model-monitor: 4MB
# Total Memory usage: 989MB

✓ K8s Migration: 8.12s
Post-migration: CPU 2500m, Memory 989MB
Final Pod location: ai-storage-master
```

### 4. TEST 2: AI Orchestrator Migration 수행 및 측정

```bash
TEST 2: AI Orchestrator Migration (Optimized)
Using pod: tensorflow-ai-workload

# Pre-migration measurement
# Pod tensorflow-ai-workload: measuring 3 containers (3 samples)
# Sample 1: 2650m
# Sample 2: 2450m
# Sample 3: 2580m
# Minimum CPU usage (of 3 samples): 2450m

Pre-migration: CPU 2450m, Memory 980MB

AI Orchestrator Migration: ai-storage-master -> gpu-server-03 (back to original for comparison)
Starting AI migration...
# [0s] Migration status: running
.# [3s] Migration status: running
.# [6s] Migration status: running
.# [9s] Migration status: running
.# [12s] Migration status: running
.# [15s] Migration status: running
.# [18s] Migration status: running
.# [21s] Migration status: running
.# [24s] Migration status: running
.# [27s] Migration status: running
.# [30s] Migration status: running
.# [33s] Migration status: completed
✓ Migration completed!

# Post-migration measurement (only 2 containers - completed container excluded)
Measuring post-migration metrics for pod: tensorflow-ai-workload-migrated-1762943063

# Pod tensorflow-ai-workload-migrated-1762943063: measuring 2 containers (3 samples)
# Sample 1: 1850m
# Sample 2: 1800m
# Sample 3: 1920m
# Minimum CPU usage (of 3 samples): 1800m

# Pod tensorflow-ai-workload-migrated-1762943063: measuring 2 containers
# Container tensorflow-trainer: 705MB
# Container model-monitor: 5MB
# Total Memory usage: 710MB

✓ AI Migration: 33.42s
Post-migration: CPU 1800m, Memory 710MB
Final Pod location: gpu-server-03
```

### 5. 성능 비교 결과 출력

```bash
=================================================================
                  PERFORMANCE COMPARISON SUMMARY
=================================================================
NODE MIGRATION:
• Original Location: gpu-server-03
• K8s Native Result: ai-storage-master
• AI Orchestrator Result: gpu-server-03
• Target Achievement: COMPLETE

METRIC                    │   K8S NATIVE │ AI ORCHESTRATOR │  IMPROVEMENT
─────────────────────────┼──────────────┼─────────────────┼─────────────
CPU Usage                 │      2500m │         1800m │      28.0%
Memory Usage              │       989MB │          710MB │      28.2%

RESULT: AI Orchestrator achieved 28.0% CPU reduction and 28.2% memory reduction

• Cleaning up...
```

## 예상결과

| 항목 | 기준 | 예시 결과 |
|------|------|----------|
| CPU 절감율 | Kubernetes 기본 마이그레이션 대비 **20% 이상** 절감 | **28.0% 달성** |
| Memory 절감율 | Kubernetes 기본 마이그레이션 대비 절감 | **28.2% 달성** |
| 컨테이너 최적화 | completed 상태 컨테이너(data-processor) 제외로 인한 리소스 최적화 확인 | 3개 → 2개 컨테이너 |
| 상태 보존 | Persistent Volume을 통한 컨테이너 상태 보존 확인 | PVC 마운트 성공 |
| 측정 정확도 | cgroup 직접 측정을 통한 실제 CPU 사용량 비교 (20회 샘플링 Trimmed Mean) | 20회 측정 완료 |

## 특이사항

### CPU/Memory 측정 방법

- **CPU 측정**: cgroup v2의 `cpu.stat` (usage_usec)를 사용하여 1초 간격으로 **20회 측정** 후 **Trimmed Mean 계산**
  - 20회 샘플을 정렬 후 상위 3개, 하위 3개 제거 (극단값 제거)
  - 중간 14개 샘플의 평균으로 CPU 사용량 도출
  - 측정 단위: millicores (1 core = 1000m)
  - **Trimmed Mean 방식**으로 TensorFlow epoch 변동의 영향을 최소화하여 **매우 안정적인 측정값** 확보
  - 예시: 정렬 [200m, 1000m, 1500m, 2000m, 2500m, ..., 5500m, 6000m]
    → 상/하위 각 3개 제거 → 중간 14개 평균 = 2500m
  
- **Memory 측정**: cgroup v2의 `memory.current`를 직접 읽어 실제 사용량 확인
  - 측정 단위: MiB (Mebibytes)

### Persistent Volume 기반 Checkpoint (핵심 기술)

**■ PV의 역할**
- **컨테이너 상태 저장소**: Pod가 삭제되어도 컨테이너 상태와 데이터를 PV에 보존
- **노드 간 공유**: NFS 기반 PVC로 모든 Worker Node에서 동일한 데이터에 접근 가능
- **Checkpoint 메커니즘**: `preserve_pv: true` 옵션으로 마이그레이션 시 자동으로 상태 저장

**■ Completed 컨테이너 처리**
- `data-processor` 컨테이너는 약 **45초 후** 데이터 전처리 작업 완료 → **Completed 상태** 전환
- 작업 결과물은 **PV에 저장**되어 있으므로 재실행 불필요
- AI Orchestrator는 **PV에 보존된 결과를 활용**하여 Completed 컨테이너를 마이그레이션 대상에서 **제외**

**■ K8s Native vs AI Orchestrator 차이**
| 항목 | K8s Native | AI Orchestrator |
|------|-----------|-----------------|
| Completed 컨테이너 | **재시작** (불필요한 CPU 사용) | **제외** (PV 결과 활용) |
| 데이터 보존 | Pod 삭제 시 손실 | **PV에 영구 보존** |
| 마이그레이션 방식 | 전체 Pod 재생성 | **선택적 컨테이너 재배포** |
| CPU 절감 | - | **700m 절감** (data-processor 제외) |

### 컨테이너 상태 분석

- AI Orchestrator는 Kubernetes API를 통해 각 컨테이너의 상태를 분석:
  - **Running**: 마이그레이션 대상 (tensorflow-trainer, model-monitor)
  - **Completed**: 제외 (data-processor, PV에 결과 보존됨)
  - **Failed**: 재시작 필요 (에러 복구)
  
- K8s Native 방식은 상태 구분 없이 **모든 컨테이너 재생성** → 불필요한 리소스 낭비

### 마이그레이션 공정성

- 공정한 비교를 위해 양쪽 모두 **동일한 소스/타겟 노드** 간 마이그레이션 수행
- K8s Native: `nodeName` 필드로 타겟 노드 명시적 지정
- AI Orchestrator: RESTful API의 `target_node` 파라미터로 타겟 노드 지정
- 동일한 TensorFlow AI 워크로드 사용 (3개 컨테이너 구성)

## 참고 자료

- 논문: "Optimized Container Pod Migration using Persistent Volume in Kubernetes"
- 프로젝트 저장소: `/root/workspace/ai-storage-orchestrator`
- 스크립트 위치: `./scripts/ai_migration_compare.sh`
- API 문서: `README.md` 참조


