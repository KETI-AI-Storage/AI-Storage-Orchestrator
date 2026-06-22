package manta

import (
	"context"
	"fmt"
)

// BackendErrorCode는 Manta 백엔드 오류 유형을 나타냅니다.
type BackendErrorCode string

const (
	BackendUnavailable BackendErrorCode = "BACKEND_UNAVAILABLE"
	NotImplemented     BackendErrorCode = "NOT_IMPLEMENTED"
)

// BackendError는 Manta 통합 레이어에서 사용하는 구조화된 오류입니다.
type BackendError struct {
	Code    BackendErrorCode
	Op      string
	Message string
	Err     error
}

func (e *BackendError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s (%s): %v", e.Op, e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s (%s)", e.Op, e.Code, e.Message)
}

func (e *BackendError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func IsBackendUnavailable(err error) bool {
	if be, ok := err.(*BackendError); ok {
		return be.Code == BackendUnavailable
	}
	return false
}

func IsNotImplemented(err error) bool {
	if be, ok := err.(*BackendError); ok {
		return be.Code == NotImplemented
	}
	return false
}

// ============================================
// 1차 통합 범위: 워크로드/데이터셋 정보 전달용 요청·응답 타입
// KETI는 스토리지 제어를 하지 않고, 정보만 MantaFS에 전달합니다.
// ============================================

// PrepareDatasetRequest는 워크로드 실행 정보를 MantaFS에 전달할 때 사용합니다.
type PrepareDatasetRequest struct {
	WorkloadID   string   // 워크로드 식별자
	Namespace    string   // 네임스페이스
	PodName      string   // Pod 이름
	DatasetName  string   // 데이터셋 식별자 (임시: PVC 이름 등. TODO(insight/apollo): 실제 데이터셋 메타데이터로 교체)
	NodeName     string   // 실행/예정 노드
	JobType      string   // 작업 유형 (예: migration, caching, provisioning)
	AccessPattern string  // 접근 패턴 (예: sequential_read, random_write)
	DataPaths    []string // 데이터 경로 목록
}

// PrepareDatasetResponse는 PrepareDataset 호출 결과입니다.
type PrepareDatasetResponse struct {
	Accepted bool   // MantaFS가 요청을 수락했는지
	Message  string // 선택적 메시지
}

// ReleaseDatasetRequest는 데이터셋 사용 종료 알림용입니다.
type ReleaseDatasetRequest struct {
	WorkloadID  string // 워크로드 식별자
	Namespace   string // 네임스페이스
	DatasetName string // 데이터셋 식별자
	Reason      string // 종료 사유
}

// PodPlacementReportRequest는 Pod 스케줄링 결과 전달용입니다.
type PodPlacementReportRequest struct {
	PodName   string // Pod 이름
	Namespace string // 네임스페이스
	NodeName  string // 배치된 노드
}

// PodPlacementReportResponse는 ReportPodPlacement 호출 결과입니다.
type PodPlacementReportResponse struct {
	Accepted bool
	Message  string
}

// DatasetUsageReportRequest는 데이터셋 사용 정보 전달용입니다.
type DatasetUsageReportRequest struct {
	DatasetName   string   // 데이터셋 식별자
	WorkloadID    string   // 워크로드 식별자
	AccessPattern string   // 접근 패턴
	DataPaths     []string // 데이터 경로 목록
}

// DatasetUsageReportResponse는 ReportDatasetUsage 호출 결과입니다.
type DatasetUsageReportResponse struct {
	Accepted bool
	Message  string
}

// IntegrationClient는 1차 통합 범위: KETI → MantaFS 정보 전달용 인터페이스입니다.
// 캐시 워밍, 티어 제어, 스토리지 부하 조회 등은 이번 범위에 포함되지 않습니다.
type IntegrationClient interface {
	// PrepareDataset 워크로드 실행 정보 전달
	PrepareDataset(ctx context.Context, req *PrepareDatasetRequest) (*PrepareDatasetResponse, error)
	// ReleaseDatasetHint 데이터셋 사용 종료 알림
	ReleaseDatasetHint(ctx context.Context, req *ReleaseDatasetRequest) error
	// ReportPodPlacement Pod 스케줄링 결과 전달
	ReportPodPlacement(ctx context.Context, req *PodPlacementReportRequest) (*PodPlacementReportResponse, error)
	// ReportDatasetUsage 데이터셋 사용 정보 전달
	ReportDatasetUsage(ctx context.Context, req *DatasetUsageReportRequest) (*DatasetUsageReportResponse, error)
}
