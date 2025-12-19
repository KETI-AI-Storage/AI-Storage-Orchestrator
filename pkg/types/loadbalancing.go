package types

import "time"

// LoadbalancingRequest represents a request to rebalance pods across nodes
type LoadbalancingRequest struct {
	// Namespace to target for loadbalancing (empty means all namespaces)
	Namespace string `json:"namespace,omitempty"`

	// TargetNodes is a list of node names to consider for loadbalancing
	// If empty, all nodes will be considered
	TargetNodes []string `json:"target_nodes,omitempty"`

	// Strategy defines the loadbalancing strategy
	// Options: "least_loaded", "load_spreading", "storage_aware"
	Strategy string `json:"strategy"`

	// Thresholds for triggering loadbalancing
	CPUThreshold    int32 `json:"cpu_threshold,omitempty"`    // Percentage (default: 80)
	MemoryThreshold int32 `json:"memory_threshold,omitempty"` // Percentage (default: 80)
	GPUThreshold    int32 `json:"gpu_threshold,omitempty"`    // Percentage (default: 80)

	// Storage I/O thresholds for AI/ML workloads
	StorageReadThreshold  int64 `json:"storage_read_threshold,omitempty"`  // MB/s threshold (default: 500)
	StorageWriteThreshold int64 `json:"storage_write_threshold,omitempty"` // MB/s threshold (default: 200)
	StorageIOPSThreshold  int64 `json:"storage_iops_threshold,omitempty"`  // IOPS threshold (default: 5000)

	// MaxMigrationsPerCycle limits how many pods can be migrated in one cycle
	MaxMigrationsPerCycle int32 `json:"max_migrations_per_cycle,omitempty"` // default: 5

	// DryRun mode only calculates recommendations without executing migrations
	DryRun bool `json:"dry_run,omitempty"`

	// Interval for periodic loadbalancing (in seconds, 0 means one-time)
	Interval int32 `json:"interval,omitempty"`

	// PreservePV indicates whether to preserve PersistentVolumes during migration
	PreservePV bool `json:"preserve_pv,omitempty"`
}

// LoadbalancingResponse represents the response after initiating loadbalancing
type LoadbalancingResponse struct {
	LoadbalancingID string                `json:"loadbalancing_id"`
	Status          LoadbalancingStatus   `json:"status"`
	Message         string                `json:"message,omitempty"`
	Details         *LoadbalancingDetails `json:"details,omitempty"`
}

// LoadbalancingStatus represents the status of a loadbalancing job
type LoadbalancingStatus string

const (
	LoadbalancingStatusPending    LoadbalancingStatus = "pending"
	LoadbalancingStatusAnalyzing  LoadbalancingStatus = "analyzing"
	LoadbalancingStatusExecuting  LoadbalancingStatus = "executing"
	LoadbalancingStatusCompleted  LoadbalancingStatus = "completed"
	LoadbalancingStatusFailed     LoadbalancingStatus = "failed"
	LoadbalancingStatusCancelled  LoadbalancingStatus = "cancelled"
)

// LoadbalancingDetails contains detailed information about a loadbalancing job
type LoadbalancingDetails struct {
	CreatedAt       time.Time                  `json:"created_at"`
	UpdatedAt       *time.Time                 `json:"updated_at,omitempty"`
	CompletedAt     *time.Time                 `json:"completed_at,omitempty"`

	// Cluster state before loadbalancing
	InitialState    ClusterState               `json:"initial_state"`

	// Planned migrations
	PlannedMigrations []MigrationPlan          `json:"planned_migrations,omitempty"`

	// Execution results
	ExecutedMigrations []MigrationResult       `json:"executed_migrations,omitempty"`

	// Statistics
	TotalPodsAnalyzed   int32                  `json:"total_pods_analyzed"`
	PodsToMigrate       int32                  `json:"pods_to_migrate"`
	SuccessfulMigrations int32                 `json:"successful_migrations"`
	FailedMigrations    int32                  `json:"failed_migrations"`

	// Resource metrics improvement
	ResourceImprovement *ResourceImprovement   `json:"resource_improvement,omitempty"`

	// Error message if failed
	ErrorMessage    string                     `json:"error_message,omitempty"`
}

// ClusterState represents the resource utilization state of the cluster
type ClusterState struct {
	Timestamp     time.Time           `json:"timestamp"`
	Nodes         []NodeState         `json:"nodes"`
	TotalPods     int32               `json:"total_pods"`
	BalanceScore  float64             `json:"balance_score"` // 0-100, higher is more balanced
}

// NodeState represents the resource state of a single node
type NodeState struct {
	NodeName       string `json:"node_name"`
	CPUPercent     int32  `json:"cpu_percent"`
	MemoryPercent  int32  `json:"memory_percent"`
	GPUPercent     int32  `json:"gpu_percent"`
	PodCount       int32  `json:"pod_count"`
	CPUCapacity    string `json:"cpu_capacity"`
	MemoryCapacity string `json:"memory_capacity"`
	GPUCapacity    int32  `json:"gpu_capacity"`
	Layer          string `json:"layer,omitempty"` // orchestration, compute, storage

	// Storage I/O metrics for AI/ML workloads
	StorageReadMBps  int64 `json:"storage_read_mbps"`  // Current read throughput in MB/s
	StorageWriteMBps int64 `json:"storage_write_mbps"` // Current write throughput in MB/s
	StorageIOPS      int64 `json:"storage_iops"`       // Current I/O operations per second
	StorageUtilization int32 `json:"storage_utilization"` // Storage utilization percentage (0-100)
}

// MigrationPlan represents a planned pod migration
type MigrationPlan struct {
	PodName           string  `json:"pod_name"`
	PodNamespace      string  `json:"pod_namespace"`
	SourceNode        string  `json:"source_node"`
	TargetNode        string  `json:"target_node"`
	Reason            string  `json:"reason"`
	Priority          int32   `json:"priority"` // Higher priority migrations executed first
	EstimatedImprovement float64 `json:"estimated_improvement"` // Expected balance score improvement
}

// MigrationResult represents the result of an executed migration
type MigrationResult struct {
	MigrationID       string    `json:"migration_id"`
	PodName           string    `json:"pod_name"`
	PodNamespace      string    `json:"pod_namespace"`
	SourceNode        string    `json:"source_node"`
	TargetNode        string    `json:"target_node"`
	Status            string    `json:"status"` // success, failed
	StartTime         time.Time `json:"start_time"`
	EndTime           time.Time `json:"end_time"`
	Duration          float64   `json:"duration_seconds"`
	ErrorMessage      string    `json:"error_message,omitempty"`
}

// ResourceImprovement tracks the improvement in resource utilization
type ResourceImprovement struct {
	CPUVarianceBefore       float64 `json:"cpu_variance_before"`
	CPUVarianceAfter        float64 `json:"cpu_variance_after"`
	MemoryVarianceBefore    float64 `json:"memory_variance_before"`
	MemoryVarianceAfter     float64 `json:"memory_variance_after"`
	GPUVarianceBefore       float64 `json:"gpu_variance_before"`
	GPUVarianceAfter        float64 `json:"gpu_variance_after"`
	BalanceScoreImprovement float64 `json:"balance_score_improvement"` // Percentage improvement

	// Storage I/O variance improvements
	StorageReadVarianceBefore  float64 `json:"storage_read_variance_before"`
	StorageReadVarianceAfter   float64 `json:"storage_read_variance_after"`
	StorageWriteVarianceBefore float64 `json:"storage_write_variance_before"`
	StorageWriteVarianceAfter  float64 `json:"storage_write_variance_after"`
	StorageIOPSVarianceBefore  float64 `json:"storage_iops_variance_before"`
	StorageIOPSVarianceAfter   float64 `json:"storage_iops_variance_after"`
}

// LoadbalancingMetrics contains overall loadbalancing metrics
type LoadbalancingMetrics struct {
	TotalLoadbalancingJobs    int32   `json:"total_loadbalancing_jobs"`
	ActiveLoadbalancingJobs   int32   `json:"active_loadbalancing_jobs"`
	TotalMigrationsExecuted   int32   `json:"total_migrations_executed"`
	SuccessfulMigrations      int32   `json:"successful_migrations"`
	FailedMigrations          int32   `json:"failed_migrations"`
	AverageBalanceScore       float64 `json:"average_balance_score"`
	LastLoadbalancingTime     *time.Time `json:"last_loadbalancing_time,omitempty"`
}

// LoadbalancingStrategy defines the strategy for loadbalancing
type LoadbalancingStrategy string

const (
	// StrategyLeastLoaded moves pods to least loaded nodes
	StrategyLeastLoaded LoadbalancingStrategy = "least_loaded"

	// StrategyLoadSpreading spreads pods evenly across all nodes
	StrategyLoadSpreading LoadbalancingStrategy = "load_spreading"

	// StrategyStorageAware prioritizes storage layer nodes
	StrategyStorageAware LoadbalancingStrategy = "storage_aware"

	// StrategyWeighted considers weighted combination of all resources
	StrategyWeighted LoadbalancingStrategy = "weighted"

	// LBStrategyStorageIOBalanced balances nodes based on Storage I/O metrics
	// Useful for AI/ML workloads with heavy data loading requirements
	LBStrategyStorageIOBalanced LoadbalancingStrategy = "storage_io_balanced"

	// LBStrategyStorageAwareWeighted combines compute and storage I/O metrics
	// Uses weighted scoring: CPU (25%), Memory (25%), GPU (20%), Storage I/O (30%)
	LBStrategyStorageAwareWeighted LoadbalancingStrategy = "storage_aware_weighted"
)
