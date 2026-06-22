package types

import "time"

// PodRef represents a reference to a pod with namespace
type PodRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// MigrationRequest represents a pod migration request
type MigrationRequest struct {
	// Source pod information
	PodName      string `json:"pod_name,omitempty"`
	PodNamespace string `json:"pod_namespace,omitempty"`
	SourceNode   string `json:"source_node,omitempty"`

	// Workload-based target information
	WorkloadName      string `json:"workload_name,omitempty"`
	WorkloadNamespace string `json:"workload_namespace,omitempty"`
	WorkloadType      string `json:"workload_type,omitempty"` // auto, Deployment, StatefulSet, Job, Workflow, Pod

	// Target node information
	TargetNode string `json:"target_node" binding:"required"`

	// Migration options
	PreservePV         bool                   `json:"preserve_pv,omitempty"`
	ForceRestart       bool                   `json:"force_restart,omitempty"`
	Timeout            int                    `json:"timeout,omitempty"` // seconds
	Stage              string                 `json:"stage,omitempty"`
	RunID              string                 `json:"run_id,omitempty"`
	PolicyName         string                 `json:"policy_name,omitempty"`
	Action             string                 `json:"action,omitempty"`
	ResourceRequests   map[string]string      `json:"resource_requests,omitempty"`
	SchedulerName      string                 `json:"scheduler_name,omitempty"`
	NodeSelector       map[string]string      `json:"node_selector,omitempty"`
	Affinity           map[string]interface{} `json:"affinity,omitempty"`
	QueueLabel         string                 `json:"queue_label,omitempty"`
	StorageAnnotations map[string]string      `json:"storage_annotations,omitempty"`
	PolicyAnnotations  map[string]string      `json:"policy_annotations,omitempty"`
}

// MigrationResponse represents the response for a migration request
type MigrationResponse struct {
	MigrationID string            `json:"migration_id"`
	Status      MigrationStatus   `json:"status"`
	Message     string            `json:"message"`
	Details     *MigrationDetails `json:"details,omitempty"`
}

// MigrationStatus represents the current status of a migration
type MigrationStatus string

const (
	MigrationStatusPending   MigrationStatus = "pending"
	MigrationStatusRunning   MigrationStatus = "running"
	MigrationStatusCompleted MigrationStatus = "completed"
	MigrationStatusFailed    MigrationStatus = "failed"
	MigrationStatusCancelled MigrationStatus = "cancelled"
)

// MigrationDetails contains detailed information about the migration process
type MigrationDetails struct {
	StartTime time.Time      `json:"start_time"`
	EndTime   *time.Time     `json:"end_time,omitempty"`
	Duration  *time.Duration `json:"duration,omitempty"`

	// Resource usage before migration
	OriginalResources *ResourceUsage `json:"original_resources,omitempty"`
	// Resource usage after migration
	OptimizedResources *ResourceUsage `json:"optimized_resources,omitempty"`

	// Container status information
	ContainerStates []ContainerState `json:"container_states,omitempty"`

	// PV checkpoint information
	CheckpointPath string `json:"checkpoint_path,omitempty"`
	PVClaimName    string `json:"pv_claim_name,omitempty"`

	// New pod information after migration
	NewPodName   string            `json:"new_pod_name,omitempty"`
	CurrentStage string            `json:"current_stage,omitempty"`
	StageHistory []StageTransition `json:"stage_history,omitempty"`
	ErrorMessage string            `json:"error_message,omitempty"`
}

type StageTransition struct {
	Stage      string    `json:"stage"`
	Status     string    `json:"status"`
	Path       string    `json:"path"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Message    string    `json:"message,omitempty"`
}

// ResourceUsage represents CPU and memory usage
type ResourceUsage struct {
	CPUUsage    float64   `json:"cpu_usage"`    // CPU cores
	MemoryUsage int64     `json:"memory_usage"` // bytes
	Timestamp   time.Time `json:"timestamp"`
}

// ContainerState represents the state of a container during migration
type ContainerState struct {
	Name          string `json:"name"`
	State         string `json:"state"` // waiting, running, completed
	RestartCount  int32  `json:"restart_count"`
	ShouldMigrate bool   `json:"should_migrate"` // whether this container should be migrated
}

// MigrationMetrics represents performance metrics for migrations
type MigrationMetrics struct {
	TotalMigrations      int64         `json:"total_migrations"`
	SuccessfulMigrations int64         `json:"successful_migrations"`
	FailedMigrations     int64         `json:"failed_migrations"`
	AverageDuration      time.Duration `json:"average_duration"`
	CPUSavings           float64       `json:"cpu_savings_percentage"`
	MemorySavings        float64       `json:"memory_savings_percentage"`
}
