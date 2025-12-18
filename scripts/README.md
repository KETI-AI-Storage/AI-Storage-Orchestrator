# AI Storage Orchestrator Scripts

이 디렉토리는 AI Storage Orchestrator의 빌드, 배포 및 테스트를 위한 스크립트를 포함합니다.

## 📁 디렉토리 구조

```
scripts/
├── 1.build-image.sh          # 컨테이너 이미지 빌드
├── 2.apply-deoloyment.sh     # Kubernetes 배포 적용
├── 3.trace-log.sh            # 로그 추적
├── build.sh                  # Go 바이너리 빌드
├── deploy.sh                 # 배포 스크립트
└── feature-tests/            # 기능 테스트 스크립트 모음
    ├── ai_migration_compare.sh         # Pod Migration 성능 비교 테스트
    ├── arch.sh                         # 아키텍처 테스트
    ├── check_pv.sh                     # PV/PVC 체크
    ├── cleanup-autoscaling-test.sh     # 오토스케일링 테스트 정리
    ├── demo-zero-downtime.sh           # Zero-downtime 마이그레이션 데모
    ├── README-AUTOSCALING-TEST.md      # 오토스케일링 테스트 가이드
    ├── show-migration-info.sh          # 마이그레이션 정보 표시
    ├── simulate-gpu-load.sh            # GPU 부하 시뮬레이션
    └── test-autoscaling.sh             # 오토스케일링 기능 테스트
```

## 🚀 빌드 및 배포 스크립트

### 1.build-image.sh
Docker 이미지를 빌드하고 레지스트리에 푸시합니다.

**사용법:**
```bash
./1.build-image.sh
```

### 2.apply-deoloyment.sh
Kubernetes 클러스터에 Orchestrator를 배포합니다.

**사용법:**
```bash
./2.apply-deoloyment.sh
```

### 3.trace-log.sh
실행 중인 Orchestrator의 로그를 실시간으로 추적합니다.

**사용법:**
```bash
./3.trace-log.sh
```

### build.sh
Go 바이너리를 빌드합니다. 선택적으로 태그를 지정할 수 있습니다.

**사용법:**
```bash
./build.sh [tag]

# 예시
./build.sh          # latest 태그로 빌드
./build.sh v1.2.0   # v1.2.0 태그로 빌드
```

### deploy.sh
빌드된 이미지를 Kubernetes 클러스터에 배포합니다.

**사용법:**
```bash
./deploy.sh [tag]

# 예시
./deploy.sh          # latest 태그 배포
./deploy.sh v1.2.0   # v1.2.0 태그 배포
```

## 🧪 기능 테스트 스크립트

모든 기능 테스트 관련 스크립트는 [`feature-tests/`](feature-tests/) 디렉토리에 있습니다.

### Pod Migration 테스트

- **ai_migration_compare.sh**: AI Storage Orchestrator의 최적화된 마이그레이션과 기본 Kubernetes 마이그레이션 성능 비교
- **check_pv.sh**: PersistentVolume 및 PersistentVolumeClaim 상태 확인
- **demo-zero-downtime.sh**: Zero-downtime 마이그레이션 시연
- **show-migration-info.sh**: 진행 중인 마이그레이션 상태 및 정보 표시

### 오토스케일링 테스트

- **test-autoscaling.sh**: 오토스케일링 기능 전체 워크플로우 자동 테스트
- **simulate-gpu-load.sh**: 다양한 GPU 부하 시나리오 시뮬레이션
- **cleanup-autoscaling-test.sh**: 오토스케일링 테스트 후 리소스 정리
- **README-AUTOSCALING-TEST.md**: 오토스케일링 테스트 상세 가이드

**자세한 내용은 [feature-tests/README-AUTOSCALING-TEST.md](feature-tests/README-AUTOSCALING-TEST.md)를 참고하세요.**

### 기타 테스트

- **arch.sh**: 시스템 아키텍처 및 구성 요소 테스트

## 📝 일반적인 워크플로우

### 개발 워크플로우

```bash
# 1. 코드 수정 후 빌드
./build.sh

# 2. 컨테이너 이미지 생성
./1.build-image.sh

# 3. Kubernetes에 배포
./2.apply-deoloyment.sh

# 4. 로그 확인
./3.trace-log.sh
```

### 오토스케일링 기능 테스트

```bash
# 1. 오토스케일링 테스트 실행
cd feature-tests
./test-autoscaling.sh

# 2. (선택) 다른 터미널에서 부하 시뮬레이션
./simulate-gpu-load.sh

# 3. 테스트 완료 후 정리
./cleanup-autoscaling-test.sh
```

### Pod Migration 기능 테스트

```bash
# 1. 마이그레이션 데모 실행
cd feature-tests
./demo-zero-downtime.sh

# 2. 마이그레이션 상태 확인
./show-migration-info.sh

# 3. 성능 비교 테스트
./ai_migration_compare.sh
```

## 🔧 트러블슈팅

### 빌드 실패

```bash
# Go 모듈 정리
go mod tidy
go mod download

# 다시 빌드
./build.sh
```

### 배포 실패

```bash
# 기존 배포 확인
kubectl get pods -n kube-system -l app=ai-storage-orchestrator

# 기존 배포 삭제 후 재배포
kubectl delete deployment ai-storage-orchestrator -n kube-system
./2.apply-deoloyment.sh
```

### 로그 확인

```bash
# 실시간 로그 추적
./3.trace-log.sh

# 또는 kubectl로 직접 확인
kubectl logs -n kube-system -l app=ai-storage-orchestrator -f

# 이전 로그 확인 (크래시 시)
kubectl logs -n kube-system -l app=ai-storage-orchestrator --previous
```

## 📚 관련 문서

- [CLAUDE.md](../CLAUDE.md) - 프로젝트 전체 개요
- [Autoscaling API Guide](../docs/autoscaling_api_guide.md) - 오토스케일링 API 가이드
- [DCGM Setup Guide](../docs/dcgm_setup_guide.md) - GPU 메트릭 수집 설정
- [Feature Tests Guide](feature-tests/README-AUTOSCALING-TEST.md) - 기능 테스트 상세 가이드

## 💡 팁

1. **병렬 개발**: 여러 터미널을 열어 빌드, 배포, 로그 추적을 동시에 수행
2. **빠른 테스트**: 로컬 변경사항을 빠르게 테스트하려면 `build.sh && ./1.build-image.sh && ./2.apply-deoloyment.sh` 체인 사용
3. **로그 필터링**: `./3.trace-log.sh | grep ERROR` 같이 로그 필터링 활용
4. **이미지 태그 관리**: 프로덕션 배포 시 `latest` 대신 명시적 버전 태그 사용
