package gluesys

import "context"

// DatasetContext 는 Gluesys MantaFS 연동 시 항상 함께 전달되는 공통 컨텍스트이다.
// 실제 외부 API 호출이 아닌 로그 기반 데모를 위해 단순하지만 사람이 읽기 좋은 필드를 유지한다.
type DatasetContext struct {
	// 워크로드 / 데이터셋 식별 정보
	WorkloadName string // 예: Job / Deployment / Pod 이름
	DatasetName  string // 예: MantaFS 상의 dataset 이름 또는 PVC 이름

	// 쿠버네티스 컨텍스트
	Namespace string
	NodeName  string

	// 스토리지 볼륨 정보 (있으면 설정, 없으면 빈 문자열)
	PVCName string
	PVName  string
}

// DatasetUsage 는 ReportDatasetUsage 에서 사용하는 데이터 사용 정보를 표현한다.
type DatasetUsage struct {
	// 데모용 단순 메트릭
	TotalBytesRead    int64
	TotalBytesWritten int64

	// 예: sequential, random, mixed 등 (자유 텍스트)
	AccessPattern string

	// 선택적 메모: 특이사항, 오류 등
	Note string
}

// Integration 은 KETI Orchestrator 가 Gluesys MantaFS 와 통신할 때 사용하는
// 통합 인터페이스이며, 실제 구현은 stub / mock / real client 로 교체 가능하다.
type Integration interface {
	// PrepareDataset
	// - 워크로드 실행 전에 호출
	// - 필요한 데이터셋을 대상 노드로 prefetch / mount 하기 위한 사전 준비 요청
	PrepareDataset(ctx context.Context, dctx DatasetContext) error

	// ReleaseDatasetHint
	// - 워크로드 / PVC 사용이 끝난 시점에 호출
	// - 해당 데이터셋을 더 이상 strong priority 로 유지하지 않아도 된다는 힌트
	ReleaseDatasetHint(ctx context.Context, dctx DatasetContext) error

	// ReportPodPlacement
	// - Pod 가 실제로 어느 노드에 스케줄링 되었는지 보고
	// - 스케줄링 결과에 따라 캐싱/배치 정책을 최적화하는 용도
	ReportPodPlacement(ctx context.Context, dctx DatasetContext) error

	// ReportDatasetUsage
	// - 실제 데이터 접근 패턴 / 사용량을 주기적으로 또는 이벤트 기반으로 보고
	// - 캐싱 정책 및 용량 계획에 활용
	ReportDatasetUsage(ctx context.Context, dctx DatasetContext, usage DatasetUsage) error
}

