package types

import "time"

// AutoscalingRequest represents a request to configure autoscaling for a workload
type AutoscalingRequest struct {
	// Target workload information
	WorkloadName      string `json:"workload_name" binding:"required"`
	WorkloadNamespace string `json:"workload_namespace" binding:"required"`
	WorkloadType      string `json:"workload_type" binding:"required"` // Deployment, StatefulSet, etc.

	// Scaling parameters
	MinReplicas    int32   `json:"min_replicas" binding:"required"`
	MaxReplicas    int32   `json:"max_replicas" binding:"required"`
	TargetCPU      int32   `json:"target_cpu_percent,omitempty"`      // Target CPU utilization percentage
	TargetMemory   int32   `json:"target_memory_percent,omitempty"`   // Target memory utilization percentage
	TargetGPU      int32   `json:"target_gpu_percent,omitempty"`      // Target GPU utilization percentage

	// Advanced settings
	ScaleUpPolicy   *ScalingPolicy `json:"scale_up_policy,omitempty"`
	ScaleDownPolicy *ScalingPolicy `json:"scale_down_policy,omitempty"`
}

// ScalingPolicy defines the policy for scaling operations
type ScalingPolicy struct {
	StabilizationWindowSeconds int32 `json:"stabilization_window_seconds,omitempty"` // Time to wait before scaling
	SelectPolicy               string `json:"select_policy,omitempty"`                // Max, Min, Disabled
	MaxScaleChange             int32  `json:"max_scale_change,omitempty"`             // Maximum number of replicas to change at once
}

// AutoscalingResponse represents the response for an autoscaling request
type AutoscalingResponse struct {
	AutoscalingID string             `json:"autoscaling_id"`
	Status        AutoscalingStatus  `json:"status"`
	Message       string             `json:"message"`
	Details       *AutoscalingDetails `json:"details,omitempty"`
}

// AutoscalingStatus represents the current status of autoscaling
type AutoscalingStatus string

const (
	AutoscalingStatusActive   AutoscalingStatus = "active"
	AutoscalingStatusInactive AutoscalingStatus = "inactive"
	AutoscalingStatusFailed   AutoscalingStatus = "failed"
)

// AutoscalingDetails contains detailed information about the autoscaling configuration
type AutoscalingDetails struct {
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        *time.Time         `json:"updated_at,omitempty"`
	CurrentReplicas  int32              `json:"current_replicas"`
	DesiredReplicas  int32              `json:"desired_replicas"`

	// Current metrics
	CurrentCPU       int32              `json:"current_cpu_percent,omitempty"`
	CurrentMemory    int32              `json:"current_memory_percent,omitempty"`
	CurrentGPU       int32              `json:"current_gpu_percent,omitempty"`

	// Scaling events
	LastScaleTime    *time.Time         `json:"last_scale_time,omitempty"`
	ScaleUpCount     int64              `json:"scale_up_count"`
	ScaleDownCount   int64              `json:"scale_down_count"`

	// HPA name (if using Kubernetes HPA)
	HPAName          string             `json:"hpa_name,omitempty"`
}

// AutoscalingMetrics represents metrics for autoscaling operations
type AutoscalingMetrics struct {
	TotalAutoscalers      int64   `json:"total_autoscalers"`
	ActiveAutoscalers     int64   `json:"active_autoscalers"`
	TotalScaleUps         int64   `json:"total_scale_ups"`
	TotalScaleDowns       int64   `json:"total_scale_downs"`
	AverageCPUUtilization float64 `json:"average_cpu_utilization"`
}
