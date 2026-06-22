package etri

import "time"

// EtriWorkloadMetadata represents metadata that ETRI sends to KETI
// to describe a workload, its datasets, and execution intent.
type EtriWorkloadMetadata struct {
	WorkloadName string            `json:"workload_name" binding:"required"`
	Namespace    string            `json:"namespace" binding:"required"`
	JobType      string            `json:"job_type,omitempty"`      // job/pod/task type
	Stage        string            `json:"stage,omitempty"`         // preprocess / train / inference
	Framework    string            `json:"framework,omitempty"`     // kubeflow, kueue, plain, etc.
	PipelineID   string            `json:"pipeline_id,omitempty"`   // optional higher-level pipeline identifier
	Labels       map[string]string `json:"labels,omitempty"`        // arbitrary labels from ETRI
	Annotations  map[string]string `json:"annotations,omitempty"`   // arbitrary annotations from ETRI
	Datasets     []DatasetReference `json:"datasets,omitempty"`     // one or more datasets
	Execution    ExecutionRequirement `json:"execution,omitempty"`  // execution-level requirements
	Scheduling   SchedulingHint       `json:"scheduling,omitempty"` // scheduling hints
	Orchestration OrchestrationHint   `json:"orchestration,omitempty"` // orchestration hints
}

// DatasetReference represents a logical dataset identifier and access pattern.
type DatasetReference struct {
	DatasetName  string            `json:"dataset_name,omitempty"`
	DatasetID    string            `json:"dataset_id,omitempty"`
	LogicalPath  string            `json:"logical_path,omitempty"` // logical path or mount hint
	AccessPattern string           `json:"access_pattern,omitempty"` // read/write/mixed/sequential/random
	CachePreferred bool            `json:"cache_preferred,omitempty"`
	LocalFirst     bool            `json:"local_first,omitempty"`
	ApproxSizeBytes int64          `json:"approx_size_bytes,omitempty"`
	FileCount      int64           `json:"file_count,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
}

// ExecutionRequirement captures compute and runtime requirements.
type ExecutionRequirement struct {
	GPUCount          int32  `json:"gpu_count,omitempty"`
	CPU               string `json:"cpu,omitempty"`    // e.g. "4", "500m"
	Memory            string `json:"memory,omitempty"` // e.g. "16Gi"
	Distributed       bool   `json:"distributed,omitempty"`
	PriorityClassName string `json:"priority_class_name,omitempty"`
	QoSClass          string `json:"qos_class,omitempty"`
}

// SchedulingHint captures locality and policy hints for the scheduler.
type SchedulingHint struct {
	LocalityPreference string            `json:"locality_preference,omitempty"` // e.g. "HIGH", "LOW", "CACHE"
	PreferCSD          bool              `json:"prefer_csd,omitempty"`
	PreferGPU          bool              `json:"prefer_gpu,omitempty"`
	AdditionalLabels   map[string]string `json:"additional_labels,omitempty"`
	AdditionalAnnotations map[string]string `json:"additional_annotations,omitempty"`
}

// OrchestrationHint captures hints for migration/checkpointing/caching behavior.
type OrchestrationHint struct {
	EnableMigration   bool   `json:"enable_migration,omitempty"`
	EnableCheckpoint  bool   `json:"enable_checkpoint,omitempty"`
	EnableGlobalCache bool   `json:"enable_global_cache,omitempty"`
	Strategy          string `json:"strategy,omitempty"` // custom high-level hint
}

// WorkloadStatusPhase represents a simplified workload phase.
type WorkloadStatusPhase string

const (
	WorkloadPhasePending   WorkloadStatusPhase = "Pending"
	WorkloadPhaseRunning   WorkloadStatusPhase = "Running"
	WorkloadPhaseFailed    WorkloadStatusPhase = "Failed"
	WorkloadPhaseSucceeded WorkloadStatusPhase = "Succeeded"
	WorkloadPhaseUnknown   WorkloadStatusPhase = "Unknown"
)

// WorkloadStatusResponse is returned to ETRI when querying workload status.
type WorkloadStatusResponse struct {
	WorkloadName string              `json:"workload_name"`
	Namespace    string              `json:"namespace"`
	Phase        WorkloadStatusPhase `json:"phase"`
	NodeName     string              `json:"node_name,omitempty"`

	// Data locality evaluation result from KETI side (e.g. HIGH / LOW / CACHE)
	Locality    string `json:"locality,omitempty"`
	DatasetName string `json:"dataset_name,omitempty"`

	// Additional human-readable information
	Message string `json:"message,omitempty"`

	// Timestamp and trace / correlation identifiers
	LastTransitionTime *time.Time        `json:"last_transition_time,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	Annotations        map[string]string `json:"annotations,omitempty"`
}

// WorkloadRef represents a minimal reference used by APIs.
type WorkloadRef struct {
	WorkloadName string `json:"workload_name" binding:"required"`
	Namespace    string `json:"namespace" binding:"required"`
}

