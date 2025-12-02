# 테스트 구성도

## AI 학습 컨테이너 마이그레이션 CPU 절감율 시험

### 테스트 환경 개요

1. 본 테스트는 **Kubernetes 클러스터 기반**의 컨테이너 오케스트레이션 환경에서 구성된다.

2. Kubernetes 클러스터는 **1개의 Master Node**와 **2개 이상의 Worker Node**로 구성되며, **AI Storage Orchestrator**가 Master Node의 kube-system 네임스페이스에 배포된다.

3. Kubernetes 클러스터는 **NFS 서버**를 통해 **Persistent Volume(PV)**을 제공하며, 이를 통해 컨테이너의 상태를 노드 간 공유 가능한 스토리지에 저장한다.

4. Kubernetes 클러스터는 **TensorFlow AI 학습 워크로드**를 실행하는 Pod를 제공하며, 해당 Pod는 **3개의 컨테이너**(tensorflow-trainer, data-processor, model-monitor)로 구성된다.

5. 각 Worker Node는 **containerd 런타임**과 **cgroup v2**를 통해 컨테이너를 실행하며, **CPU/Memory 사용량을 직접 측정**할 수 있다.

6. 테스트 수행 과정은 **Test Client**(또는 Master Node)를 통해 Kubernetes 클러스터에 **SSH로 접속**하여 마이그레이션 성능 비교 스크립트(`ai_migration_compare.sh`)를 실행하고, 결과를 확인한다.

---

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                                                                                         │
│  ┌─────────────────────── Kubernetes Cluster ────────────────────────────────────┐     │
│  │                                                                                │     │
│  │  ┌──────────────── Master Node ─────────────────┐                             │     │
│  │  │ 호스트명: ai-storage-master                   │                             │     │
│  │  │ 프로세서: 32 코어 이상                         │                             │     │
│  │  │ 메모리: 64 GiB 이상                           │                             │     │
│  │  │ OS 스토리지: 128 GiB 이상                     │                             │     │
│  │  │ OS: Linux Kernel 6.14 이상                   │                             │     │
│  │  │ Container Runtime: containerd                │                             │     │
│  │  │                                               │                             │     │
│  │  │ ┌─── AI Storage Orchestrator ────┐           │                             │     │
│  │  │ │ Namespace: kube-system          │           │                             │     │
│  │  │ │ Port: 8080 (RESTful API)        │           │                             │     │
│  │  │ │ 역할: Migration Controller       │           │                             │     │
│  │  │ │ 기능:                            │           │                             │     │
│  │  │ │ - 컨테이너 상태 분석             │           │                             │     │
│  │  │ │ - PV 기반 Checkpoint            │           │                             │     │
│  │  │ │ - 최적화 Pod 재배포             │           │                             │     │
│  │  │ └─────────────────────────────────┘           │                             │     │
│  │  └───────────────────────────────────────────────┘                             │     │
│  │                          │                                                     │     │
│  │                          │ Kubernetes API                                     │     │
│  │                          │                                                     │     │
│  │  ┌───────────────────────┴──────────────────────────────────────────────┐    │     │
│  │  │                                                                       │    │     │
│  │  │  ┌──────────── Worker Node 1 ────────────┐                           │    │     │
│  │  │  │ 호스트명: gpu-server-03               │                           │    │     │
│  │  │  │ 프로세서: 32 코어 이상                 │                           │    │     │
│  │  │  │ 메모리: 64 GiB 이상                   │                           │    │     │
│  │  │  │ OS 스토리지: 256 GiB 이상             │                           │    │     │
│  │  │  │ 네트워크: 10GbE 이상                  │                           │    │     │
│  │  │  │ NFS Client: nfs-common 설치 필요       │                           │    │     │
│  │  │  │                                        │                           │    │     │
│  │  │  │ ┌─ TensorFlow AI Pod (초기) ──┐       │                           │    │     │
│  │  │  │ │ Name: tensorflow-ai-workload │       │                           │    │     │
│  │  │  │ │                               │       │                           │    │     │
│  │  │  │ │ ┌── Container 1 ───────────┐ │       │                           │    │     │
│  │  │  │ │ │ tensorflow-trainer        │ │       │                           │    │     │
│  │  │  │ │ │ State: Running            │ │       │                           │    │     │
│  │  │  │ │ │ CPU: 1500m, Mem: 3Gi     │ │       │                           │    │     │
│  │  │  │ │ └───────────────────────────┘ │       │                           │    │     │
│  │  │  │ │                               │       │                           │    │     │
│  │  │  │ │ ┌── Container 2 ───────────┐ │       │                           │    │     │
│  │  │  │ │ │ data-processor            │ │       │                           │    │     │
│  │  │  │ │ │ State: Completed (45s)    │ │       │                           │    │     │
│  │  │  │ │ │ CPU: 500m, Mem: 1.5Gi    │ │       │                           │    │     │
│  │  │  │ │ └───────────────────────────┘ │       │                           │    │     │
│  │  │  │ │                               │       │                           │    │     │
│  │  │  │ │ ┌── Container 3 ───────────┐ │       │                           │    │     │
│  │  │  │ │ │ model-monitor             │ │       │                           │    │     │
│  │  │  │ │ │ State: Running            │ │       │                           │    │     │
│  │  │  │ │ │ CPU: 200m, Mem: 512Mi    │ │       │                           │    │     │
│  │  │  │ │ └───────────────────────────┘ │       │                           │    │     │
│  │  │  │ └───────────────────────────────┘       │                           │    │     │
│  │  │  └────────────────────────────────────────┘                           │    │     │
│  │  │                                                                         │    │     │
│  │  │  ┌──────────── Worker Node 2+ ──────────┐                             │    │     │
│  │  │  │ 호스트명: ai-storage-worker-NN        │                             │    │     │
│  │  │  │ 프로세서: 32 코어 이상                 │                             │    │     │
│  │  │  │ 메모리: 64 GiB 이상                   │                             │    │     │
│  │  │  │ OS 스토리지: 256 GiB 이상             │                             │    │     │
│  │  │  │ 네트워크: 10GbE 이상                  │                             │    │     │
│  │  │  │ NFS Client: nfs-common 설치 필요       │                             │    │     │
│  │  │  │                                        │                             │    │     │
│  │  │  │ (마이그레이션 타겟 노드)                │                             │    │     │
│  │  │  └────────────────────────────────────────┘                           │    │     │
│  │  │                                                                         │    │     │
│  │  └─────────────────────────────────────────────────────────────────────────┘    │     │
│  │                                                                                │     │
│  │  ┌─────────────────── NFS Server ───────────────────┐                        │     │
│  │  │ 호스트명: nfs-server (또는 ai-storage-master)     │                        │     │
│  │  │ 스토리지: 1 TiB 이상                              │                        │     │
│  │  │ 네트워크: 10GbE 이상                              │                        │     │
│  │  │ Export Path: /nfs/pv-storage                     │                        │     │
│  │  │                                                   │                        │     │
│  │  │ ┌─── Persistent Volume (PV) ─────────┐           │                        │     │
│  │  │ │ Type: NFS                           │           │                        │     │
│  │  │ │ Capacity: 5Gi                       │           │                        │     │
│  │  │ │ Access Mode: ReadWriteMany          │           │                        │     │
│  │  │ │ 용도: Container Checkpoint Storage   │           │                        │     │
│  │  │ │                                      │           │                        │     │
│  │  │ │ ┌─ PersistentVolumeClaim ─────┐     │           │                        │     │
│  │  │ │ │ Name: tensorflow-checkpoint-pvc│  │           │                        │     │
│  │  │ │ │ Mounted: /migration-checkpoint │  │           │                        │     │
│  │  │ │ └────────────────────────────────┘  │           │                        │     │
│  │  │ └─────────────────────────────────────┘           │                        │     │
│  │  └───────────────────────────────────────────────────┘                        │     │
│  │                                                                                │     │
│  └────────────────────────────────────────────────────────────────────────────────┘     │
│                                                                                         │
│  ┌──────────── Test Client (SSH) ────────────┐                                         │
│  │ 호스트명: test-client 또는 ai-storage-master│                                         │
│  │ OS: Linux                                  │                                         │
│  │                                             │                                         │
│  │ ┌─── 시험 도구 ──────────────────────┐      │                                         │
│  │ │ - ai_migration_compare.sh          │      │                                         │
│  │ │ - kubectl (Kubernetes CLI)         │      │                                         │
│  │ │ - curl (RESTful API 호출)          │      │                                         │
│  │ │ - ssh (노드 접근)                   │      │                                         │
│  │ └────────────────────────────────────┘      │                                         │
│  └─────────────────────────────────────────────┘                                         │
│                                                                                         │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

## 마이그레이션 흐름도

```
┌────────────────────────────────────────────────────────────────────────────────┐
│                         마이그레이션 프로세스                                   │
└────────────────────────────────────────────────────────────────────────────────┘

┌─── K8s Native Migration (기존 방식) ───────────────────────────────────────┐
│                                                                             │
│  [Worker Node 1: gpu-server-03]                                            │
│     ┌─────────────────────────┐                                            │
│     │ Pod (3 containers)      │                                            │
│     │ - trainer (Running)     │                                            │
│     │ - processor (Completed) │ ◄─── Completed 컨테이너도 포함             │
│     │ - monitor (Running)     │                                            │
│     └─────────────────────────┘                                            │
│               │                                                             │
│               │ kubectl delete pod                                          │
│               ▼                                                             │
│         [Pod 삭제]                                                          │
│               │                                                             │
│               │ kubectl apply -f                                            │
│               ▼                                                             │
│  [Worker Node 2: ai-storage-worker-01]                                     │
│     ┌─────────────────────────┐                                            │
│     │ Pod (3 containers)      │ ◄─── 모든 컨테이너 재시작                   │
│     │ - trainer (Restarted)   │      (Cold Start!)                         │
│     │ - processor (Restarted) │ ◄─── 불필요한 재시작 (CPU/Memory 낭비!)    │
│     │ - monitor (Restarted)   │                                            │
│     └─────────────────────────┘                                            │
│                                                                             │
│  결과: CPU 2500m, Memory 989MB                                              │
└─────────────────────────────────────────────────────────────────────────────┘

┌─── AI Orchestrator Migration (최적화 방식) ────────────────────────────────┐
│                                                                             │
│  [Worker Node 1: gpu-server-03]                                            │
│     ┌─────────────────────────┐                                            │
│     │ Pod (3 containers)      │                                            │
│     │ - trainer (Running)     │ ◄─── Running 컨테이너 식별                 │
│     │ - processor (Completed) │ ◄─── Completed 컨테이너 식별 (제외 대상!)  │
│     │ - monitor (Running)     │ ◄─── Running 컨테이너 식별                 │
│     └─────────────────────────┘                                            │
│               │                                                             │
│               │ POST /api/migrations                                        │
│               │ {target_node, preserve_pv: true}                            │
│               ▼                                                             │
│     [AI Storage Orchestrator]                                               │
│     - 컨테이너 상태 분석                                                     │
│     - PV에 Checkpoint 저장                                                  │
│     - Running 컨테이너만 선택                                                │
│               │                                                             │
│               ▼                                                             │
│  [Worker Node 2: ai-storage-worker-01]                                     │
│     ┌─────────────────────────┐                                            │
│     │ Pod (2 containers)      │ ◄─── Running 컨테이너만 재배포              │
│     │ - trainer (Restored)    │      (PV에서 상태 복원)                    │
│     │ - monitor (Restored)    │                                            │
│     └─────────────────────────┘                                            │
│          │                                                                  │
│          └─ processor (Completed) ◄─── PV에 결과 저장, 재실행 안함!        │
│                                                                             │
│  결과: CPU 1800m (▼700m), Memory 710MB (▼279MB)                             │
│  개선: CPU 28.0% 절감, Memory 28.2% 절감                                    │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 테스트 환경 상세 요구사항

### 하드웨어 요구사항

| 구성요소 | 최소 사양 | 권장 사양 | 비고 |
|---------|----------|----------|------|
| **Master Node** | CPU 16 Core<br>RAM 32GB<br>Storage 128GB | CPU 32 Core<br>RAM 64GB<br>Storage 256GB | Control Plane + Orchestrator |
| **Worker Node** (최소 2대) | CPU 16 Core<br>RAM 32GB<br>Storage 256GB | CPU 32 Core<br>RAM 64GB<br>Storage 512GB | 컨테이너 워크로드 실행 |
| **NFS Server** | Storage 1TB<br>Network 1GbE | Storage 2TB<br>Network 10GbE | Persistent Volume 제공 |
| **네트워크** | 1GbE 스위치 | 10GbE 스위치 | 저지연 마이그레이션 |

### 소프트웨어 요구사항

| 항목 | 버전 | 필수 여부 | 용도 |
|------|------|-----------|------|
| **Kubernetes** | v1.28 이상 | 필수 | 컨테이너 오케스트레이션 |
| **containerd** | v1.7 이상 | 필수 | 컨테이너 런타임 |
| **Linux Kernel** | 6.14 이상 | 필수 | cgroup v2 지원 |
| **NFS Client** | nfs-common | 필수 | PV 마운트 (모든 Worker Node) |
| **Docker** | v24 이상 | 필수 | 이미지 빌드 (Master Node) |
| **kubectl** | v1.28 이상 | 필수 | Kubernetes CLI |
| **TensorFlow** | 2.13.0 | 필수 | AI 워크로드 이미지 |

### 네트워크 요구사항

| 항목 | 설정 | 비고 |
|------|------|------|
| **노드 간 통신** | 10GbE 이상 | 빠른 마이그레이션 |
| **NFS 통신** | 10GbE 이상 | PV 접근 성능 |
| **Container Network** | CNI (Calico/Flannel) | Pod 간 통신 |
| **Service Network** | ClusterIP, NodePort | 서비스 노출 |
| **방화벽** | 6443 (API), 8080 (Orchestrator) | 포트 개방 필요 |

### 스토리지 요구사항

| 항목 | 용량 | 타입 | 용도 |
|------|------|------|------|
| **OS Storage** | 128GB 이상 | SSD | 운영체제 (모든 노드) |
| **Container Storage** | 256GB 이상 | SSD | 컨테이너 이미지 (Worker Node) |
| **NFS Storage** | 1TB 이상 | HDD/SSD | Persistent Volume |
| **PV per Pod** | 5GB 이상 | NFS | Checkpoint 저장 |

## 시스템 구성 요소 상세

### 1. Kubernetes 클러스터 구성

| 구성요소 | 사양 | 역할 |
|---------|------|------|
| Master Node | CPU 32 Core, RAM 64GB, Storage 128GB | - Control Plane<br>- AI Storage Orchestrator 호스팅<br>- API Server, Scheduler, Controller Manager |
| Worker Node 1 | CPU 32 Core, RAM 64GB, Storage 256GB | - 컨테이너 워크로드 실행<br>- 마이그레이션 소스 노드 |
| Worker Node 2+ | CPU 32 Core, RAM 64GB, Storage 256GB | - 컨테이너 워크로드 실행<br>- 마이그레이션 타겟 노드 |

### 2. 네트워크 구성

| 항목 | 요구사항 |
|------|---------|
| 노드 간 네트워크 | 10GbE 이상 (저지연 마이그레이션) |
| Container Network | CNI (Calico, Flannel 등) |
| Service Network | ClusterIP, NodePort |
| NFS Network | 10GbE 이상 (PV 접근) |

### 3. 스토리지 구성

| 항목 | 타입 | 용량 | 용도 |
|------|------|------|------|
| OS Storage | Local SSD | 128-256GB | 운영체제 및 시스템 파일 |
| Container Storage | containerd | - | 컨테이너 이미지 및 런타임 |
| Persistent Volume | NFS | 5Gi+ | 컨테이너 Checkpoint 저장 |

### 4. 소프트웨어 구성

| 소프트웨어 | 버전 | 용도 |
|-----------|------|------|
| Kubernetes | v1.28+ | 컨테이너 오케스트레이션 |
| containerd | v1.7+ | 컨테이너 런타임 |
| Linux Kernel | 6.14+ | cgroup v2 지원 |
| NFS Client | nfs-common | PV 마운트 |
| TensorFlow | 2.13.0 | AI 워크로드 |

## 테스트 시나리오 흐름

```
1. [Test Client] ./ai_migration_compare.sh 실행
         │
         ├─► 2. [Master Node] TensorFlow Pod 배포 (gpu-server-03)
         │         │
         │         ├─ tensorflow-trainer: Running
         │         ├─ data-processor: Running → Completed (45초)
         │         └─ model-monitor: Running
         │
         ├─► 3. [TEST 1] K8s Native Migration
         │         │
         │         ├─ Pre-measurement: cgroup 측정 (3회 샘플링)
         │         ├─ kubectl delete pod
         │         ├─ kubectl apply -f (nodeName: ai-storage-worker-01)
         │         └─ Post-measurement: CPU 2500m, Memory 989MB
         │
         ├─► 4. [TEST 2] AI Orchestrator Migration
         │         │
         │         ├─ Pre-measurement: cgroup 측정 (3회 샘플링)
         │         ├─ POST /api/migrations
         │         │   {
         │         │     "pod_name": "tensorflow-ai-workload",
         │         │     "source_node": "ai-storage-worker-01",
         │         │     "target_node": "gpu-server-03",
         │         │     "preserve_pv": true
         │         │   }
         │         ├─ Orchestrator: 상태 분석 → Checkpoint → 최적화 재배포
         │         └─ Post-measurement: CPU 1800m, Memory 710MB
         │
         └─► 5. [Result] 성능 비교 출력
                   CPU 절감: 28.0%
                   Memory 절감: 28.2%
```

## 주요 특징

1. **컨테이너 상태 기반 최적화**
   - Running 컨테이너만 마이그레이션
   - Completed 컨테이너 제외 → CPU/Memory 절감

2. **Persistent Volume 활용**
   - NFS 기반 공유 스토리지
   - 컨테이너 Checkpoint 보존
   - 노드 간 상태 공유

3. **정확한 성능 측정**
   - cgroup v2 직접 측정
   - 20회 샘플링 후 Trimmed Mean 계산
   - 극단값 제거로 매우 안정적인 측정값 확보

4. **공정한 비교**
   - 동일 소스/타겟 노드
   - 동일 워크로드
   - 동일 측정 방법

5. **성능 목표 달성**
   - CPU 절감율: 28.0% (목표 20% 초과 달성)
   - Memory 절감율: 28.2%
   - 1차년도 목표 달성

