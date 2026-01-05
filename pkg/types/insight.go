package types

import "time"

// InsightReport represents a workload signature report from insight-trace sidecar
type InsightReport struct {
	TraceID      string                 `json:"trace_id"`
	PodName      string                 `json:"pod_name"`
	PodNamespace string                 `json:"pod_namespace"`
	Signature    *WorkloadSignature     `json:"signature"`
	Timestamp    time.Time              `json:"timestamp"`
}

// WorkloadSignature represents the workload characteristics collected by insight-trace
type WorkloadSignature struct {
	// Pod identification
	PodName      string `json:"pod_name"`
	PodNamespace string `json:"pod_namespace"`
	NodeName     string `json:"node_name,omitempty"`

	// Workload type and stage
	WorkloadType   string `json:"workload_type,omitempty"`   // training, inference, preprocessing, etc.
	CurrentStage   string `json:"current_stage,omitempty"`   // init, running, completed, etc.

	// I/O characteristics
	IOPattern        string  `json:"io_pattern,omitempty"`         // read-heavy, write-heavy, balanced
	AvgReadBytesPS   float64 `json:"avg_read_bytes_per_sec,omitempty"`
	AvgWriteBytesPS  float64 `json:"avg_write_bytes_per_sec,omitempty"`
	AvgReadIOPS      float64 `json:"avg_read_iops,omitempty"`
	AvgWriteIOPS     float64 `json:"avg_write_iops,omitempty"`

	// Resource usage
	CPUUsagePercent    float64 `json:"cpu_usage_percent,omitempty"`
	MemoryUsagePercent float64 `json:"memory_usage_percent,omitempty"`
	MemoryUsageBytes   int64   `json:"memory_usage_bytes,omitempty"`

	// GPU metrics (if available)
	GPUUtilization float64 `json:"gpu_utilization,omitempty"`
	GPUMemoryUsed  int64   `json:"gpu_memory_used,omitempty"`
	GPUMemoryTotal int64   `json:"gpu_memory_total,omitempty"`

	// Pipeline context (for Argo Workflows/Kubeflow Pipelines)
	WorkflowName     string `json:"workflow_name,omitempty"`
	WorkflowUID      string `json:"workflow_uid,omitempty"`
	PipelineStepName string `json:"pipeline_step_name,omitempty"`
	PipelineStepType string `json:"pipeline_step_type,omitempty"`

	// Timing
	StartTime       time.Time `json:"start_time,omitempty"`
	LastUpdated     time.Time `json:"last_updated,omitempty"`
	DurationSeconds float64   `json:"duration_seconds,omitempty"`

	// Annotations from pod
	Annotations map[string]string `json:"annotations,omitempty"`
}

// InsightReportResponse is the response after receiving an insight report
type InsightReportResponse struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// InsightMetrics holds aggregated metrics from insight reports
type InsightMetrics struct {
	TotalReports       int64                    `json:"total_reports"`
	ActiveWorkloads    int                      `json:"active_workloads"`
	ReportsByType      map[string]int64         `json:"reports_by_type"`
	ReportsByNamespace map[string]int64         `json:"reports_by_namespace"`
	LastReportTime     time.Time                `json:"last_report_time"`
	WorkloadSummaries  []WorkloadSummary        `json:"workload_summaries,omitempty"`
}

// WorkloadSummary provides a summary of a tracked workload
type WorkloadSummary struct {
	PodName      string    `json:"pod_name"`
	PodNamespace string    `json:"pod_namespace"`
	WorkloadType string    `json:"workload_type"`
	IOPattern    string    `json:"io_pattern"`
	LastUpdated  time.Time `json:"last_updated"`
}
