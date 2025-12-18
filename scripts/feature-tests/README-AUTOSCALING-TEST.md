# 오토스케일링 기능 검증 가이드

AI Storage Orchestrator의 오토스케일링 기능을 검증하기 위한 스크립트 모음입니다.

## 📋 스크립트 개요

### 1. test-autoscaling.sh (메인 테스트 스크립트)
오토스케일링 기능의 전체 워크플로우를 자동으로 테스트합니다.

**수행 작업:**
1. AI Storage Orchestrator 실행 상태 확인
2. DCGM Exporter 실행 상태 확인
3. GPU 테스트 워크로드 배포
4. GPU 노드 라벨 확인
5. Orchestrator로 포트포워드 설정 (8080 포트)
6. 오토스케일러 생성
7. 3분 동안 메트릭 모니터링 (30초마다 체크)
8. 결과 출력

**실행 방법:**
```bash
cd /root/workspace/ai-storage-orchestrator
./scripts/test-autoscaling.sh
```

**예상 출력:**
```
==========================================
AI Storage Orchestrator Autoscaling Test
==========================================

[STEP] Checking if AI Storage Orchestrator is running...
[SUCCESS] AI Storage Orchestrator is running

[STEP] Checking if DCGM Exporter is running...
[SUCCESS] DCGM Exporter is running

[STEP] Deploying GPU test workload...
[SUCCESS] GPU test workload deployed

[STEP] Creating autoscaler for GPU workload...
[SUCCESS] Autoscaler created with ID: as-12345678

[STEP] Monitoring autoscaler for 3 minutes...
----------------------------------------
Check #1 (14:30:15)
----------------------------------------
  Replicas: 1 (desired: 1)
  CPU: 5% (target: 70%)
  Memory: 12% (target: 80%)
  GPU: 0% (target: 60%)
  Scale events: UP=0, DOWN=0
```

### 2. simulate-gpu-load.sh (부하 시뮬레이션)
다양한 GPU 부하 시나리오를 시뮬레이션합니다.

**시나리오:**
- Scenario 1: 낮은 부하 (1 replica)
- Scenario 2: 중간 부하 (2 replicas)
- Scenario 3: 높은 부하 (3 replicas)

**실행 방법:**
```bash
cd /root/workspace/ai-storage-orchestrator
./scripts/simulate-gpu-load.sh
```

**사용 시기:**
- test-autoscaling.sh 실행 중 다른 터미널에서 실행
- 오토스케일러의 스케일 업/다운 동작을 테스트하고 싶을 때

### 3. cleanup-autoscaling-test.sh (정리 스크립트)
테스트 후 리소스를 정리합니다.

**수행 작업:**
1. 모든 오토스케일러 삭제
2. 포트포워드 프로세스 종료
3. GPU 테스트 워크로드 삭제 (선택사항)
4. DCGM Exporter 삭제 (선택사항)

**실행 방법:**
```bash
cd /root/workspace/ai-storage-orchestrator
./scripts/cleanup-autoscaling-test.sh
```

## 🚀 빠른 시작

### 전체 테스트 실행 (3분 소요)

```bash
cd /root/workspace/ai-storage-orchestrator

# 1. 메인 테스트 실행
./scripts/test-autoscaling.sh

# 2. 결과 확인 후 정리
./scripts/cleanup-autoscaling-test.sh
```

### 부하 시뮬레이션 포함 테스트

**터미널 1 (모니터링):**
```bash
cd /root/workspace/ai-storage-orchestrator
./scripts/test-autoscaling.sh
```

**터미널 2 (부하 생성):**
```bash
cd /root/workspace/ai-storage-orchestrator
# 메인 테스트가 모니터링 단계에 들어간 후 실행
./scripts/simulate-gpu-load.sh
```

## 📊 메트릭 설명

### 출력 메트릭 해석

```
Replicas: 1 (desired: 2)
```
- **current replicas**: 현재 실행 중인 Pod 수
- **desired replicas**: 오토스케일러가 원하는 Pod 수
- 두 값이 다르면 스케일링이 진행 중

```
CPU: 5% (target: 70%)
Memory: 12% (target: 80%)
GPU: 0% (target: 60%)
```
- **current**: 현재 평균 사용률
- **target**: 목표 사용률
- 현재값이 target보다 높으면 scale up 가능성
- 현재값이 target보다 낮으면 scale down 가능성

```
Scale events: UP=2, DOWN=1
```
- **UP**: 스케일 업 발생 횟수
- **DOWN**: 스케일 다운 발생 횟수

## 🔍 예상 동작

### 정상 시나리오

1. **초기 상태 (1 replica)**
   - GPU 사용률: 0-10% (nvidia-smi만 실행)
   - 오토스케일러: 현재 상태 유지

2. **부하 증가 시**
   - GPU 사용률이 60% 초과
   - 30초 후: 스케일 업 권장
   - Stabilization window 후: 실제 스케일 업 수행

3. **부하 감소 시**
   - GPU 사용률이 60% 미만
   - 5분 동안 낮은 사용률 유지 (기본 stabilization window)
   - 이후: 스케일 다운 수행

### GPU 메트릭 소스

**DCGM Exporter 사용 가능 시:**
- 실제 GPU 사용률 수집
- Pod별 GPU 메트릭 제공
- 정확한 스케일링 결정

**DCGM Exporter 사용 불가 시:**
- Fallback: 시뮬레이션 값 (60-90%)
- 로그에 "Failed to get real metrics" 메시지 출력
- 오토스케일링은 계속 동작

## 🐛 트러블슈팅

### 문제: "AI Storage Orchestrator is not running"

**원인:** Orchestrator가 배포되지 않았거나 중단됨

**해결:**
```bash
cd /root/workspace/ai-storage-orchestrator
./scripts/build.sh
./scripts/deploy.sh
kubectl wait --for=condition=available --timeout=120s deployment/ai-storage-orchestrator -n kube-system
```

### 문제: "DCGM Exporter is not running"

**원인:** DCGM Exporter가 배포되지 않음

**해결:**
```bash
kubectl apply -f /root/workspace/ai-storage-orchestrator/deployments/dcgm-exporter.yaml
kubectl wait --for=condition=ready --timeout=120s pod -l app=dcgm-exporter -n gpu-monitoring
```

### 문제: "No GPU nodes found"

**원인:** GPU 노드에 필요한 라벨이 없음

**해결:**
```bash
# GPU가 있는 노드 이름 확인
kubectl get nodes

# 라벨 추가
kubectl label nodes <gpu-node-name> nvidia.com/gpu=present
```

### 문제: "Failed to connect to orchestrator"

**원인:** 포트포워드 실패

**해결:**
```bash
# 기존 포트포워드 종료
pkill -f "kubectl port-forward.*ai-storage-orchestrator"

# 수동으로 포트포워드 설정
kubectl port-forward -n kube-system svc/ai-storage-orchestrator 8080:8080

# 다른 터미널에서 테스트
curl http://localhost:8080/health
```

### 문제: "GPU utilization always 0%"

**예상 동작:** 테스트 워크로드는 nvidia-smi만 실행하므로 GPU 사용률이 낮습니다.

**실제 GPU 부하 생성:**
```bash
# GPU 연산 부하를 생성하는 Pod 배포
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: gpu-burn
spec:
  containers:
  - name: gpu-burn
    image: nvidia/cuda:11.8.0-base-ubuntu22.04
    command: ["sh", "-c", "apt-get update && apt-get install -y git build-essential && git clone https://github.com/wilicc/gpu-burn && cd gpu-burn && make && ./gpu_burn 300"]
    resources:
      limits:
        nvidia.com/gpu: 1
EOF
```

## 📝 수동 테스트

스크립트 없이 수동으로 테스트하려면:

### 1. 포트포워드 설정
```bash
kubectl port-forward -n kube-system svc/ai-storage-orchestrator 8080:8080
```

### 2. 오토스케일러 생성
```bash
curl -X POST http://localhost:8080/api/v1/autoscalers \
  -H "Content-Type: application/json" \
  -d '{
    "workload_name": "gpu-test-workload",
    "workload_namespace": "default",
    "workload_type": "deployment",
    "min_replicas": 1,
    "max_replicas": 5,
    "target_cpu_percent": 70,
    "target_memory_percent": 80,
    "target_gpu_percent": 60,
    "scale_check_interval": 30
  }'
```

### 3. 상태 확인
```bash
# 모든 오토스케일러 조회
curl http://localhost:8080/api/v1/autoscalers

# 특정 오토스케일러 상세 조회 (ID는 생성 시 반환된 값)
curl http://localhost:8080/api/v1/autoscalers/<autoscaling-id>
```

### 4. 오토스케일러 삭제
```bash
curl -X DELETE http://localhost:8080/api/v1/autoscalers/<autoscaling-id>
```

## 📈 성능 지표

테스트에서 확인할 수 있는 지표:

1. **반응 시간**: 부하 변화 후 스케일링까지의 시간
2. **안정성**: Flapping 없이 안정적으로 동작하는지
3. **정확성**: GPU 메트릭이 실제 사용률을 반영하는지
4. **Stabilization Window**: 스케일 다운이 너무 급격하지 않은지

## 🔗 관련 문서

- [Autoscaling API Guide](../docs/autoscaling_api_guide.md)
- [DCGM Setup Guide](../docs/dcgm_setup_guide.md)
- [CLAUDE.md](../CLAUDE.md)

## 💡 팁

1. **로그 모니터링**: 테스트 중 로그를 보면 더 자세한 정보를 얻을 수 있습니다
   ```bash
   kubectl logs -n kube-system -l app=ai-storage-orchestrator -f
   ```

2. **실시간 모니터링**: watch 명령어로 실시간 상태 확인
   ```bash
   watch -n 5 'curl -s http://localhost:8080/api/v1/autoscalers/<id>'
   ```

3. **GPU 메트릭 직접 확인**: DCGM Exporter 메트릭 직접 조회
   ```bash
   kubectl run test-curl --image=curlimages/curl:latest --rm -i --restart=Never -- \
     curl -s http://dcgm-exporter.gpu-monitoring.svc.cluster.local:9400/metrics | grep DCGM_FI_DEV_GPU_UTIL
   ```
