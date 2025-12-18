// Package v1 contains API Schema definitions for the apollo v1 API group
// StorageHPA: AI/ML 워크로드를 위한 Storage I/O 기반 오토스케일링 CRD
package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Workload",type=string,JSONPath=`.spec.workloadRef.name`
// +kubebuilder:printcolumn:name="Min",type=integer,JSONPath=`.spec.minReplicas`
// +kubebuilder:printcolumn:name="Max",type=integer,JSONPath=`.spec.maxReplicas`
// +kubebuilder:printcolumn:name="Current",type=integer,JSONPath=`.status.currentReplicas`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// StorageHPA는 AI/ML 워크로드를 위한 Storage I/O 인식 HPA
// CPU/Memory/GPU뿐만 아니라 Storage I/O 메트릭도 고려하여 스케일링
type StorageHPA struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StorageHPASpec   `json:"spec,omitempty"`
	Status StorageHPAStatus `json:"status,omitempty"`
}

// StorageHPASpec defines the desired state of StorageHPA
// 오토스케일링 정책 정의
type StorageHPASpec struct {
	// WorkloadRef는 스케일링 대상 워크로드 참조
	// +kubebuilder:validation:Required
	WorkloadRef WorkloadReference `json:"workloadRef"`

	// MinReplicas는 최소 레플리카 수 (최소 1)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Required
	MinReplicas int32 `json:"minReplicas"`

	// MaxReplicas는 최대 레플리카 수
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Required
	MaxReplicas int32 `json:"maxReplicas"`

	// ============================================
	// 컴퓨트 리소스 타겟
	// ============================================

	// TargetCPUPercent는 목표 CPU 사용률 (1-100%)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	TargetCPUPercent *int32 `json:"targetCPUPercent,omitempty"`

	// TargetMemoryPercent는 목표 메모리 사용률 (1-100%)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	TargetMemoryPercent *int32 `json:"targetMemoryPercent,omitempty"`

	// TargetGPUPercent는 목표 GPU 사용률 (1-100%)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	TargetGPUPercent *int32 `json:"targetGPUPercent,omitempty"`

	// ============================================
	// Storage I/O 타겟 (AI/ML 워크로드 핵심!)
	// ============================================

	// TargetStorageReadThroughput는 목표 스토리지 읽기 처리량 (MB/s)
	// AI 학습 데이터 로딩에 중요
	// +kubebuilder:validation:Minimum=1
	// +optional
	TargetStorageReadThroughput *int64 `json:"targetStorageReadThroughput,omitempty"`

	// TargetStorageWriteThroughput는 목표 스토리지 쓰기 처리량 (MB/s)
	// 체크포인트 저장에 중요
	// +kubebuilder:validation:Minimum=1
	// +optional
	TargetStorageWriteThroughput *int64 `json:"targetStorageWriteThroughput,omitempty"`

	// TargetStorageIOPS는 목표 스토리지 IOPS
	// +kubebuilder:validation:Minimum=1
	// +optional
	TargetStorageIOPS *int64 `json:"targetStorageIOPS,omitempty"`

	// ============================================
	// 스케일링 정책
	// ============================================

	// ScaleUpPolicy는 스케일 업 정책
	// +optional
	ScaleUpPolicy *ScalingPolicySpec `json:"scaleUpPolicy,omitempty"`

	// ScaleDownPolicy는 스케일 다운 정책
	// +optional
	ScaleDownPolicy *ScalingPolicySpec `json:"scaleDownPolicy,omitempty"`
}

// WorkloadReference는 대상 워크로드 참조
type WorkloadReference struct {
	// Name은 워크로드 이름
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Kind는 워크로드 종류 (Deployment, StatefulSet)
	// +kubebuilder:validation:Enum=Deployment;StatefulSet
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`
}

// ScalingPolicySpec는 스케일링 정책 정의
type ScalingPolicySpec struct {
	// StabilizationWindowSeconds는 스케일링 전 안정화 대기 시간 (초)
	// 스케일 다운 기본값: 300초 (5분)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=3600
	// +optional
	StabilizationWindowSeconds *int32 `json:"stabilizationWindowSeconds,omitempty"`

	// MaxScaleChange는 한 번에 변경할 최대 레플리카 수
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxScaleChange *int32 `json:"maxScaleChange,omitempty"`
}

// StorageHPAStatus defines the observed state of StorageHPA
// 컨트롤러가 관리하는 현재 상태
type StorageHPAStatus struct {
	// ============================================
	// 레플리카 상태
	// ============================================

	// CurrentReplicas는 현재 레플리카 수
	CurrentReplicas int32 `json:"currentReplicas,omitempty"`

	// DesiredReplicas는 목표 레플리카 수
	DesiredReplicas int32 `json:"desiredReplicas,omitempty"`

	// ============================================
	// 현재 메트릭
	// ============================================

	// CurrentCPUPercent는 현재 CPU 사용률 (%)
	CurrentCPUPercent int32 `json:"currentCPUPercent,omitempty"`

	// CurrentMemoryPercent는 현재 메모리 사용률 (%)
	CurrentMemoryPercent int32 `json:"currentMemoryPercent,omitempty"`

	// CurrentGPUPercent는 현재 GPU 사용률 (%)
	CurrentGPUPercent int32 `json:"currentGPUPercent,omitempty"`

	// CurrentStorageReadThroughput는 현재 스토리지 읽기 처리량 (MB/s)
	CurrentStorageReadThroughput int64 `json:"currentStorageReadThroughput,omitempty"`

	// CurrentStorageWriteThroughput는 현재 스토리지 쓰기 처리량 (MB/s)
	CurrentStorageWriteThroughput int64 `json:"currentStorageWriteThroughput,omitempty"`

	// CurrentStorageIOPS는 현재 스토리지 IOPS
	CurrentStorageIOPS int64 `json:"currentStorageIOPS,omitempty"`

	// ============================================
	// 스케일링 이력
	// ============================================

	// LastScaleTime은 마지막 스케일링 시간
	// +optional
	LastScaleTime *metav1.Time `json:"lastScaleTime,omitempty"`

	// ScaleUpCount는 스케일 업 횟수
	ScaleUpCount int64 `json:"scaleUpCount,omitempty"`

	// ScaleDownCount는 스케일 다운 횟수
	ScaleDownCount int64 `json:"scaleDownCount,omitempty"`

	// ============================================
	// 상태 정보
	// ============================================

	// Phase는 오토스케일러 상태
	// +kubebuilder:validation:Enum=Active;Inactive;Failed
	Phase StorageHPAPhase `json:"phase,omitempty"`

	// Message는 상태 메시지
	// +optional
	Message string `json:"message,omitempty"`

	// LastUpdated는 마지막 업데이트 시간
	// +optional
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`

	// Conditions는 상세 조건들
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// StorageHPAPhase는 오토스케일러의 단계
type StorageHPAPhase string

const (
	// StorageHPAPhaseActive는 오토스케일러가 활성 상태
	StorageHPAPhaseActive StorageHPAPhase = "Active"

	// StorageHPAPhaseInactive는 오토스케일러가 비활성 상태
	StorageHPAPhaseInactive StorageHPAPhase = "Inactive"

	// StorageHPAPhaseFailed는 오토스케일러가 실패 상태
	StorageHPAPhaseFailed StorageHPAPhase = "Failed"
)

// Condition Types
const (
	// ConditionTypeReady는 오토스케일러가 준비됨
	ConditionTypeReady = "Ready"

	// ConditionTypeScaling은 현재 스케일링 중
	ConditionTypeScaling = "Scaling"

	// ConditionTypeMetricsAvailable은 메트릭이 사용 가능함
	ConditionTypeMetricsAvailable = "MetricsAvailable"
)

// +kubebuilder:object:root=true

// StorageHPAList contains a list of StorageHPA
type StorageHPAList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StorageHPA `json:"items"`
}

// ============================================
// 헬퍼 함수들
// ============================================

// GetStabilizationWindowForScaleUp returns the scale up stabilization window
func (s *StorageHPASpec) GetStabilizationWindowForScaleUp() int32 {
	if s.ScaleUpPolicy != nil && s.ScaleUpPolicy.StabilizationWindowSeconds != nil {
		return *s.ScaleUpPolicy.StabilizationWindowSeconds
	}
	return 0 // 스케일 업은 기본적으로 바로 실행
}

// GetStabilizationWindowForScaleDown returns the scale down stabilization window
func (s *StorageHPASpec) GetStabilizationWindowForScaleDown() int32 {
	if s.ScaleDownPolicy != nil && s.ScaleDownPolicy.StabilizationWindowSeconds != nil {
		return *s.ScaleDownPolicy.StabilizationWindowSeconds
	}
	return 300 // 기본 5분
}

// GetMaxScaleChangeForScaleUp returns the max scale change for scale up
func (s *StorageHPASpec) GetMaxScaleChangeForScaleUp() int32 {
	if s.ScaleUpPolicy != nil && s.ScaleUpPolicy.MaxScaleChange != nil {
		return *s.ScaleUpPolicy.MaxScaleChange
	}
	return 0 // 0은 제한 없음
}

// GetMaxScaleChangeForScaleDown returns the max scale change for scale down
func (s *StorageHPASpec) GetMaxScaleChangeForScaleDown() int32 {
	if s.ScaleDownPolicy != nil && s.ScaleDownPolicy.MaxScaleChange != nil {
		return *s.ScaleDownPolicy.MaxScaleChange
	}
	return 0 // 0은 제한 없음
}

// HasAnyTarget returns true if any target metric is set
func (s *StorageHPASpec) HasAnyTarget() bool {
	return s.TargetCPUPercent != nil ||
		s.TargetMemoryPercent != nil ||
		s.TargetGPUPercent != nil ||
		s.TargetStorageReadThroughput != nil ||
		s.TargetStorageWriteThroughput != nil ||
		s.TargetStorageIOPS != nil
}

func init() {
	// 스키마 등록은 별도 register.go에서 수행
}
