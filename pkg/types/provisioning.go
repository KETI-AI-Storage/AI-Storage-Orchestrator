package types

import "time"

// ProvisioningRequest represents a request to provision storage for AI/ML workload
type ProvisioningRequest struct {
	// Target workload information
	WorkloadName      string `json:"workload_name" binding:"required"`
	WorkloadNamespace string `json:"workload_namespace" binding:"required"`
	WorkloadType      string `json:"workload_type" binding:"required"` // training, inference, data-pipeline

	// Storage requirements
	StorageSize         string `json:"storage_size,omitempty"`          // e.g., "500Gi", "1Ti"
	StorageClass        string `json:"storage_class,omitempty"`         // e.g., "high-throughput", "high-iops", "balanced"
	AccessMode          string `json:"access_mode,omitempty"`           // ReadWriteOnce, ReadWriteMany, ReadOnlyMany
	AutoSize            bool   `json:"auto_size,omitempty"`             // Automatically determine size based on workload type

	// Performance requirements
	RequiredReadThroughput  int64 `json:"required_read_throughput_mbps,omitempty"`  // Required read throughput in MB/s
	RequiredWriteThroughput int64 `json:"required_write_throughput_mbps,omitempty"` // Required write throughput in MB/s
	RequiredIOPS            int64 `json:"required_iops,omitempty"`                  // Required IOPS

	// Advanced settings
	MountPath           string            `json:"mount_path,omitempty"`           // Mount path in container
	VolumeMode          string            `json:"volume_mode,omitempty"`          // Filesystem or Block
	Labels              map[string]string `json:"labels,omitempty"`               // Additional labels
	Annotations         map[string]string `json:"annotations,omitempty"`          // Additional annotations
}

// ProvisioningResponse represents the response for a provisioning request
type ProvisioningResponse struct {
	ProvisioningID string              `json:"provisioning_id"`
	Status         ProvisioningStatus  `json:"status"`
	Message        string              `json:"message"`
	Details        *ProvisioningDetails `json:"details,omitempty"`
}

// ProvisioningStatus represents the current status of provisioning
type ProvisioningStatus string

const (
	ProvisioningStatusPending   ProvisioningStatus = "pending"
	ProvisioningStatusCreating  ProvisioningStatus = "creating"
	ProvisioningStatusReady     ProvisioningStatus = "ready"
	ProvisioningStatusFailed    ProvisioningStatus = "failed"
	ProvisioningStatusDeleting  ProvisioningStatus = "deleting"
)

// ProvisioningDetails contains detailed information about the provisioning
type ProvisioningDetails struct {
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	ReadyAt     *time.Time `json:"ready_at,omitempty"`

	// Created resources
	PVCName         string `json:"pvc_name"`
	PVName          string `json:"pv_name,omitempty"`
	ActualSize      string `json:"actual_size"`
	ActualClass     string `json:"actual_class"`

	// Performance profile
	EstimatedReadThroughput  int64 `json:"estimated_read_throughput_mbps,omitempty"`
	EstimatedWriteThroughput int64 `json:"estimated_write_throughput_mbps,omitempty"`
	EstimatedIOPS            int64 `json:"estimated_iops,omitempty"`

	// Workload binding
	BoundToWorkload bool   `json:"bound_to_workload"`
	MountedPods     []string `json:"mounted_pods,omitempty"`
}

// ProvisioningMetrics represents metrics for provisioning operations
type ProvisioningMetrics struct {
	TotalProvisionings      int64   `json:"total_provisionings"`
	ActiveProvisionings     int64   `json:"active_provisionings"`
	TotalStorageProvisioned string  `json:"total_storage_provisioned"` // e.g., "5Ti"
	AverageProvisionTime    float64 `json:"average_provision_time_seconds"`
}

// StorageProfile defines performance characteristics for storage classes
type StorageProfile struct {
	Name                string `json:"name"`
	ReadThroughputMBps  int64  `json:"read_throughput_mbps"`
	WriteThroughputMBps int64  `json:"write_throughput_mbps"`
	IOPS                int64  `json:"iops"`
	Description         string `json:"description"`
}

// WorkloadStorageRecommendation provides storage recommendations based on workload type
type WorkloadStorageRecommendation struct {
	WorkloadType        string `json:"workload_type"`
	RecommendedSize     string `json:"recommended_size"`
	RecommendedClass    string `json:"recommended_class"`
	RecommendedProfile  StorageProfile `json:"recommended_profile"`
	Reasoning           string `json:"reasoning"`
}
