package types

import "time"

// PreemptionRequest represents a request to preempt low-priority pods
type PreemptionRequest struct {
	// NodeName is the node to free up resources on
	NodeName string `json:"node_name" binding:"required"`

	// Namespace to target for preemption (empty means all namespaces)
	Namespace string `json:"namespace,omitempty"`

	// ResourceType to free up: "cpu", "memory", "gpu", "storage", "all"
	ResourceType string `json:"resource_type" binding:"required"`

	// TargetAmount is the amount of resource to free (e.g., "4000m" for CPU, "8Gi" for memory)
	TargetAmount string `json:"target_amount" binding:"required"`

	// Strategy for selecting pods to preempt
	// Options: "lowest_priority", "youngest", "largest_resource", "weighted_score"
	Strategy string `json:"strategy,omitempty"` // default: "lowest_priority"

	// MinPriority is the minimum priority class value to consider for preemption
	// Pods with priority < MinPriority can be preempted
	MinPriority int32 `json:"min_priority,omitempty"` // default: 0

	// MaxPodsToPreempt limits the number of pods to preempt
	MaxPodsToPreempt int32 `json:"max_pods_to_preempt,omitempty"` // default: 10

	// GracePeriodSeconds for pod termination
	GracePeriodSeconds int64 `json:"grace_period_seconds,omitempty"` // default: 30

	// ProtectedNamespaces are namespaces that should not be preempted
	ProtectedNamespaces []string `json:"protected_namespaces,omitempty"`

	// Reason for preemption (for auditing)
	Reason string `json:"reason,omitempty"`
}

// PreemptionResponse represents the response after initiating preemption
type PreemptionResponse struct {
	PreemptionID string             `json:"preemption_id"`
	Status       PreemptionStatus   `json:"status"`
	Message      string             `json:"message,omitempty"`
	Details      *PreemptionDetails `json:"details,omitempty"`
}

// PreemptionStatus represents the status of a preemption operation
type PreemptionStatus string

const (
	PreemptionStatusPending   PreemptionStatus = "pending"
	PreemptionStatusAnalyzing PreemptionStatus = "analyzing"
	PreemptionStatusExecuting PreemptionStatus = "executing"
	PreemptionStatusCompleted PreemptionStatus = "completed"
	PreemptionStatusFailed    PreemptionStatus = "failed"
)

// PreemptionDetails contains detailed information about a preemption operation
type PreemptionDetails struct {
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// Node state before preemption
	InitialNodeState NodeResourceState `json:"initial_node_state"`

	// Target resource to free
	TargetResourceType   string `json:"target_resource_type"`
	TargetResourceAmount string `json:"target_resource_amount"`

	// Candidates for preemption
	PreemptionCandidates []PreemptionCandidate `json:"preemption_candidates,omitempty"`

	// Execution results
	PreemptedPods []PreemptedPodInfo `json:"preempted_pods,omitempty"`

	// Statistics
	TotalPodsAnalyzed     int32 `json:"total_pods_analyzed"`
	PodsToPreempt         int32 `json:"pods_to_preempt"`
	SuccessfulPreemptions int32 `json:"successful_preemptions"`
	FailedPreemptions     int32 `json:"failed_preemptions"`

	// Resource freed
	ResourceFreed ResourceAmount `json:"resource_freed"`

	// Whether target was achieved
	TargetAchieved bool `json:"target_achieved"`

	// Error message if failed
	ErrorMessage string `json:"error_message,omitempty"`
}

// NodeResourceState represents the resource state of a node
type NodeResourceState struct {
	NodeName         string `json:"node_name"`
	CPUAllocatable   string `json:"cpu_allocatable"`
	MemoryAllocatable string `json:"memory_allocatable"`
	GPUAllocatable   int32  `json:"gpu_allocatable"`
	CPURequested     string `json:"cpu_requested"`
	MemoryRequested  string `json:"memory_requested"`
	GPURequested     int32  `json:"gpu_requested"`
	CPUPercent       int32  `json:"cpu_percent"`
	MemoryPercent    int32  `json:"memory_percent"`
	GPUPercent       int32  `json:"gpu_percent"`
	PodCount         int32  `json:"pod_count"`
}

// PreemptionCandidate represents a pod candidate for preemption
type PreemptionCandidate struct {
	PodName          string         `json:"pod_name"`
	PodNamespace     string         `json:"pod_namespace"`
	PriorityClass    string         `json:"priority_class,omitempty"`
	PriorityValue    int32          `json:"priority_value"`
	ResourceRequests ResourceAmount `json:"resource_requests"`
	CreationTime     time.Time      `json:"creation_time"`
	Age              string         `json:"age"` // human readable age
	PreemptionScore  float64        `json:"preemption_score"` // Lower score = preempt first
	PreemptionReason string         `json:"preemption_reason"`
	Selected         bool           `json:"selected"` // Whether this pod is selected for preemption
}

// PreemptedPodInfo contains information about a preempted pod
type PreemptedPodInfo struct {
	PodName       string         `json:"pod_name"`
	PodNamespace  string         `json:"pod_namespace"`
	PriorityValue int32          `json:"priority_value"`
	PreemptedAt   time.Time      `json:"preempted_at"`
	Status        string         `json:"status"` // success, failed
	ErrorMessage  string         `json:"error_message,omitempty"`
	ResourceFreed ResourceAmount `json:"resource_freed"`
}

// ResourceAmount represents resource quantities
type ResourceAmount struct {
	CPU     string `json:"cpu,omitempty"`     // e.g., "2000m"
	Memory  string `json:"memory,omitempty"`  // e.g., "4Gi"
	GPU     int32  `json:"gpu,omitempty"`
	Storage string `json:"storage,omitempty"` // e.g., "10Gi"

	// Storage I/O metrics for AI/ML workloads
	StorageReadMBps  int64 `json:"storage_read_mbps,omitempty"`  // Read throughput in MB/s
	StorageWriteMBps int64 `json:"storage_write_mbps,omitempty"` // Write throughput in MB/s
	StorageIOPS      int64 `json:"storage_iops,omitempty"`       // I/O operations per second
}

// PreemptionMetrics contains overall preemption metrics
type PreemptionMetrics struct {
	TotalPreemptionJobs   int32      `json:"total_preemption_jobs"`
	ActivePreemptionJobs  int32      `json:"active_preemption_jobs"`
	TotalPodsPreempted    int32      `json:"total_pods_preempted"`
	SuccessfulPreemptions int32      `json:"successful_preemptions"`
	FailedPreemptions     int32      `json:"failed_preemptions"`
	TotalCPUFreed         string     `json:"total_cpu_freed"`
	TotalMemoryFreed      string     `json:"total_memory_freed"`
	TotalGPUFreed         int32      `json:"total_gpu_freed"`
	LastPreemptionTime    *time.Time `json:"last_preemption_time,omitempty"`
}

// PreemptionStrategy defines how to select pods for preemption
type PreemptionStrategy string

const (
	// StrategyLowestPriority preempts lowest priority pods first
	StrategyLowestPriority PreemptionStrategy = "lowest_priority"

	// StrategyYoungest preempts youngest pods first (minimize work loss)
	StrategyYoungest PreemptionStrategy = "youngest"

	// StrategyLargestResource preempts pods with largest resource requests first
	StrategyLargestResource PreemptionStrategy = "largest_resource"

	// StrategyWeightedScore uses weighted scoring combining priority, age, and resource
	StrategyWeightedScore PreemptionStrategy = "weighted_score"

	// StrategyStorageIOHeaviest preempts pods with highest storage I/O first
	// Useful for freeing up storage bandwidth for high-priority AI/ML workloads
	StrategyStorageIOHeaviest PreemptionStrategy = "storage_io_heaviest"

	// StrategyStorageAwareWeighted uses weighted scoring including storage I/O metrics
	// Combines priority (30%), age (20%), compute resources (25%), storage I/O (25%)
	StrategyStorageAwareWeighted PreemptionStrategy = "storage_aware_weighted"
)

// PodResourceInfo contains pod resource information for preemption decisions
type PodResourceInfo struct {
	PodName       string
	PodNamespace  string
	PriorityClass string
	PriorityValue int32
	CPURequest    int64 // millicores
	MemoryRequest int64 // bytes
	GPURequest    int32
	CreationTime  time.Time

	// Storage I/O metrics for AI/ML data-intensive workloads
	StorageReadMBps  int64 // Current read throughput in MB/s
	StorageWriteMBps int64 // Current write throughput in MB/s
	StorageIOPS      int64 // Current I/O operations per second
	PVCCount         int32 // Number of PVCs attached
	TotalPVCSize     int64 // Total PVC size in bytes
}
