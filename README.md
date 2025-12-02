# AI Storage Cluster Orchestrator

**논문 기반 Kubernetes Pod 마이그레이션 오케스트레이터**

## 개요

이 프로젝트는 **"Kubernetes에서 Persistent Volume을 사용한 최적화된 컨테이너 Pod 마이그레이션"** 연구 논문을 기반으로 구현된 AI Storage Cluster Orchestrator입니다.

### 🎯 주요 목표

- **CPU 사용량 50% 절감** - 완료된 컨테이너 제외를 통한 리소스 최적화
- **메모리 사용량 40% 절감** - 불필요한 컨테이너 메모리 절약  
- **콜드 스타트 시간 50% 단축** - PV 기반 체크포인트로 빠른 복원
- **무중단 마이그레이션** - Persistent Volume을 활용한 상태 보존

### 🏗️ 아키텍처

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Control       │    │   Compute       │    │   Storage       │  
│   Plane         │    │   Nodes         │    │   Nodes         │
│                 │    │                 │    │                 │
│ ┌─────────────┐ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│ │Orchestrator │ │    │ │    Pods     │ │    │ │     PVs     │ │
│ │             │ │    │ │             │ │    │ │             │ │
│ └─────────────┘ │    │ └─────────────┘ │    │ └─────────────┘ │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

### 🔬 최적화된 3단계 마이그레이션

1. **상태 캡처**: 컨테이너별 실행 상태 분석 (waiting/running/completed)
2. **체크포인트 저장**: Persistent Volume에 컨테이너 상태 저장
3. **최적화된 재배포**: 실행 중인 컨테이너만으로 새 Pod 생성

## 🚀 빠른 시작

### 사전 요구사항

- **Kubernetes**: 1.25+
- **Go**: 1.21+
- **Docker**: 최신 버전
- **kubectl**: 클러스터 접근 권한

### 1. 노드 라벨링

```bash
# Control Plane 노드
kubectl label nodes <master-node> layer=orchestration
kubectl label nodes <master-node> node-role.kubernetes.io/control-plane=

# Worker 노드 (Compute)
kubectl label nodes <worker-node> layer=compute  
kubectl label node <worker-node> node-role.kubernetes.io/worker=

# Storage 노드
kubectl label nodes <storage-node> layer=storage
kubectl label node <storage-node> node-role.kubernetes.io/worker=
```

### 2. 빌드 및 배포

```bash
# 저장소 클론
git clone https://github.com/KETI-AI-Storage/AI-Storage-API-Server.git
cd ai-storage-orchestrator

# 빌드 실행
./scripts/build.sh

# Kubernetes에 배포
./scripts/deploy.sh
```

### 3. 서비스 확인

```bash
# 배포 상태 확인
kubectl get pods -n kube-system -l app=ai-storage-orchestrator

# 포트 포워딩
kubectl port-forward -n kube-system svc/ai-storage-orchestrator 8080:8080

# Health Check
curl http://localhost:8080/health
```

## 📡 API 사용법

### Pod 마이그레이션 시작

```bash
curl -X POST http://localhost:8080/api/v1/migrations \
  -H "Content-Type: application/json" \
  -d '{
    "pod_name": "example-pod",
    "pod_namespace": "default",
    "source_node": "worker-1", 
    "target_node": "worker-2",
    "preserve_pv": true,
    "timeout": 600
  }'
```

### 마이그레이션 상태 조회

```bash
curl http://localhost:8080/api/v1/migrations/{migration-id}
```

### 성능 메트릭 확인

```bash
curl http://localhost:8080/api/v1/metrics
```

## 📊 성능 최적화 기대 효과

K8s 기준 대비 성능 개선:

| 메트릭 | 기존 K8s 방식 | 최적화된 방식 | K8s 기준 개선율 |
|--------|---------------|----------------|-----------------|
| CPU 사용량 | 100% | 50% | **50% 절감** |
| 메모리 사용량 | 100% | 60% | **40% 절감** |  
| 콜드 스타트 시간 | 100% | 50% | **50% 단축** |

## 🛠️ 고급 기능

### 배치 마이그레이션

여러 Pod를 순차적으로 마이그레이션:

```bash
# 스크립트 예시 (USAGE.md 참조)
for pod in app-1 app-2 app-3; do
  # 마이그레이션 API 호출
done
```

### 조건부 마이그레이션

리소스 사용량이 높은 Pod를 자동으로 마이그레이션:

```bash
# 높은 CPU 사용률의 Pod 자동 마이그레이션
kubectl top pods | awk '$2 > 100 {print $1}' | xargs -I {} ./migrate-pod.sh {}
```

### 모니터링 및 알림

실시간 성능 모니터링:

```bash
# 메트릭 수집 및 알림
watch -n 60 'curl -s http://localhost:8080/api/v1/metrics | jq'
```

## 📁 프로젝트 구조

```
ai-storage-orchestrator/
├── cmd/
│   └── main.go                    # 메인 애플리케이션
├── pkg/
│   ├── apis/
│   │   └── handler.go            # HTTP API 핸들러
│   ├── controller/
│   │   └── migration.go          # 마이그레이션 컨트롤러  
│   ├── k8s/
│   │   └── client.go             # Kubernetes 클라이언트
│   └── types/
│       └── migration.go          # 데이터 타입 정의
├── deployments/
│   └── cluster-orchestrator.yaml # K8s 배포 매니페스트
├── scripts/
│   ├── build.sh                  # 빌드 스크립트
│   ├── deploy.sh                 # 배포 스크립트
│   ├── ai_migration_compare.sh   # AI 컨테이너 성능 비교 (공인 인증)
│   └── benchmark-migration.sh    # 일반 마이그레이션 벤치마크
├── Dockerfile                     # 컨테이너 이미지 정의
├── USAGE.md                      # 상세 사용법 가이드
└── README.md                     # 이 파일
```

## 🔍 주요 구현 특징

### 1. 컨테이너 상태 기반 최적화

```go
// 최적화 핵심: 컨테이너 상태별 처리
type ContainerState struct {
    Name          string `json:"name"`
    State         string `json:"state"`         // waiting, running, completed
    ShouldMigrate bool   `json:"should_migrate"` // 마이그레이션 여부 결정
}
```

### 2. Persistent Volume 활용

- Pod 생명주기와 독립적인 데이터 보존
- 체크포인트 기반 빠른 상태 복원
- 노드 간 안전한 상태 이동

### 3. RESTful API

- 간편한 HTTP API 인터페이스
- 실시간 마이그레이션 상태 조회
- 성능 메트릭 수집 및 모니터링

## 🧪 테스트 및 검증

### AI 컨테이너 마이그레이션 성능 비교 (공인 인증)

```bash
# AI 학습 컨테이너 CPU 절감율 비교 테스트
./scripts/ai_migration_compare.sh --source-node worker1 --target-node worker2
```

**특징:**
- TensorFlow AI 워크로드 기반 실제 테스트
- K8s 네이티브 vs AI Orchestrator 정확한 비교
- 공인 인증서 형태의 결과 출력 (영문)
- CPU/메모리 절감율 정밀 측정
- KETI 공식 인증서 발급

### 성능 벤치마크

```bash
# 일반 마이그레이션 성능 측정
./scripts/benchmark-migration.sh --source-node node1 --target-node node2
```

### 단위 테스트

```bash
go test ./pkg/...
```

## 📚 문서

- **[USAGE.md](USAGE.md)** - 상세 사용법 및 고급 기능 가이드
- **API 문서** - Swagger/OpenAPI 스펙 (예정)
- **개발자 가이드** - 기여 방법 및 개발 환경 설정

## 🤝 기여

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/AmazingFeature`)
3. Commit your changes (`git commit -m 'Add some AmazingFeature'`)
4. Push to the branch (`git push origin feature/AmazingFeature`)
5. Open a Pull Request

## 📄 라이선스

이 프로젝트는 Apache 2.0 라이선스 하에 배포됩니다.

## 🙏 Acknowledgements

This work was supported by the Institute of Information & Communications Technology Planning & Evaluation(IITP) grant funded by the Korea government(MSIT) (No.RS-2024-00461572, Development of High-efficiency Parallel Storage SW Technology Optimized for AI Computational Accelerators)

---

**Developed by KETI (Korea Electronics Technology Institute)**

참고 연구: "Optimized Container Pod Migration using Persistent Volume in Kubernetes"# AI-Storage-Orchestrator
