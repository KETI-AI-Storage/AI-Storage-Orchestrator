package controller

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"ai-storage-orchestrator/pkg/types"

	"github.com/google/uuid"
)

// ProvisioningController manages storage provisioning for AI/ML workloads
type ProvisioningController struct {
	k8sClient        K8sClientInterface
	provisionings    map[string]*ProvisioningJob
	provisioningsMux sync.RWMutex
	metrics          *types.ProvisioningMetrics

	// Storage profiles for different performance requirements
	storageProfiles map[string]types.StorageProfile
}

// ProvisioningJob represents an active provisioning job
type ProvisioningJob struct {
	ID        string
	Request   *types.ProvisioningRequest
	Status    types.ProvisioningStatus
	Details   *types.ProvisioningDetails
	CreatedAt time.Time
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewProvisioningController creates a new provisioning controller
func NewProvisioningController(k8sClient K8sClientInterface) *ProvisioningController {
	pc := &ProvisioningController{
		k8sClient:     k8sClient,
		provisionings: make(map[string]*ProvisioningJob),
		metrics: &types.ProvisioningMetrics{
			TotalProvisionings:      0,
			ActiveProvisionings:     0,
			TotalStorageProvisioned: "0Gi",
			AverageProvisionTime:    0,
		},
		storageProfiles: make(map[string]types.StorageProfile),
	}

	// Initialize storage profiles
	pc.initializeStorageProfiles()

	return pc
}

// initializeStorageProfiles sets up predefined storage performance profiles
func (pc *ProvisioningController) initializeStorageProfiles() {
	pc.storageProfiles["high-throughput"] = types.StorageProfile{
		Name:                "high-throughput",
		ReadThroughputMBps:  800,
		WriteThroughputMBps: 400,
		IOPS:                5000,
		Description:         "Optimized for sequential read/write operations (AI training datasets)",
	}

	pc.storageProfiles["high-iops"] = types.StorageProfile{
		Name:                "high-iops",
		ReadThroughputMBps:  400,
		WriteThroughputMBps: 200,
		IOPS:                10000,
		Description:         "Optimized for random I/O operations (checkpoint files, metadata)",
	}

	pc.storageProfiles["balanced"] = types.StorageProfile{
		Name:                "balanced",
		ReadThroughputMBps:  500,
		WriteThroughputMBps: 250,
		IOPS:                3000,
		Description:         "Balanced performance for mixed workloads",
	}

	pc.storageProfiles["standard"] = types.StorageProfile{
		Name:                "standard",
		ReadThroughputMBps:  200,
		WriteThroughputMBps: 100,
		IOPS:                1000,
		Description:         "Standard performance for general purpose",
	}
}

// CreateProvisioning creates a new storage provisioning for AI/ML workload
func (pc *ProvisioningController) CreateProvisioning(req *types.ProvisioningRequest) (*types.ProvisioningResponse, error) {
	// Validate request
	if err := pc.validateRequest(req); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Generate unique provisioning ID
	provisioningID := fmt.Sprintf("provisioning-%s", uuid.New().String()[:8])

	// Apply auto-sizing if requested
	if req.AutoSize {
		pc.autoSizeStorage(req)
	}

	// Select appropriate storage class if not specified
	if req.StorageClass == "" {
		req.StorageClass = pc.selectStorageClass(req)
	}

	// Set default access mode if not specified
	if req.AccessMode == "" {
		req.AccessMode = "ReadWriteOnce"
	}

	// Create provisioning job
	ctx, cancel := context.WithCancel(context.Background())
	job := &ProvisioningJob{
		ID:      provisioningID,
		Request: req,
		Status:  types.ProvisioningStatusPending,
		Details: &types.ProvisioningDetails{
			CreatedAt:   time.Now(),
			PVCName:     fmt.Sprintf("pvc-%s-%s", req.WorkloadName, provisioningID[:8]),
			ActualSize:  req.StorageSize,
			ActualClass: req.StorageClass,
		},
		CreatedAt: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Add performance estimates based on storage class
	if profile, exists := pc.storageProfiles[req.StorageClass]; exists {
		job.Details.EstimatedReadThroughput = profile.ReadThroughputMBps
		job.Details.EstimatedWriteThroughput = profile.WriteThroughputMBps
		job.Details.EstimatedIOPS = profile.IOPS
	}

	// Store job
	pc.provisioningsMux.Lock()
	pc.provisionings[provisioningID] = job
	pc.metrics.TotalProvisionings++
	pc.metrics.ActiveProvisionings++
	pc.provisioningsMux.Unlock()

	// Start provisioning asynchronously
	go pc.executeProvisioning(job)

	log.Printf("Provisioning %s: Created for workload %s/%s with size %s, class %s",
		provisioningID, req.WorkloadNamespace, req.WorkloadName, req.StorageSize, req.StorageClass)

	return &types.ProvisioningResponse{
		ProvisioningID: provisioningID,
		Status:         job.Status,
		Message:        "Provisioning request accepted and processing",
		Details:        job.Details,
	}, nil
}

// executeProvisioning performs the actual storage provisioning
func (pc *ProvisioningController) executeProvisioning(job *ProvisioningJob) {
	startTime := time.Now()

	// Update status to creating
	pc.updateJobStatus(job, types.ProvisioningStatusCreating)

	// TODO: Implement actual PVC creation via Kubernetes API
	// For now, simulate provisioning
	log.Printf("Provisioning %s: Creating PVC %s with size %s",
		job.ID, job.Details.PVCName, job.Details.ActualSize)

	// Simulate provisioning delay
	time.Sleep(2 * time.Second)

	// Update status to ready
	pc.updateJobStatus(job, types.ProvisioningStatusReady)
	readyTime := time.Now()
	job.Details.ReadyAt = &readyTime
	job.Details.UpdatedAt = &readyTime

	// Update metrics
	provisionTime := time.Since(startTime).Seconds()
	pc.provisioningsMux.Lock()
	if pc.metrics.AverageProvisionTime == 0 {
		pc.metrics.AverageProvisionTime = provisionTime
	} else {
		pc.metrics.AverageProvisionTime = (pc.metrics.AverageProvisionTime + provisionTime) / 2
	}
	pc.provisioningsMux.Unlock()

	log.Printf("Provisioning %s: Completed successfully in %.2f seconds", job.ID, provisionTime)
}

// updateJobStatus updates the status of a provisioning job
func (pc *ProvisioningController) updateJobStatus(job *ProvisioningJob, status types.ProvisioningStatus) {
	pc.provisioningsMux.Lock()
	defer pc.provisioningsMux.Unlock()

	job.Status = status
	now := time.Now()
	job.Details.UpdatedAt = &now
}

// autoSizeStorage automatically determines storage size based on workload type
func (pc *ProvisioningController) autoSizeStorage(req *types.ProvisioningRequest) {
	switch req.WorkloadType {
	case "training":
		// AI training typically requires large datasets
		if req.StorageSize == "" {
			req.StorageSize = "500Gi"
		}
	case "inference":
		// Inference typically needs model files only
		if req.StorageSize == "" {
			req.StorageSize = "100Gi"
		}
	case "data-pipeline":
		// Data pipelines need intermediate storage
		if req.StorageSize == "" {
			req.StorageSize = "250Gi"
		}
	default:
		// Default for unknown workload types
		if req.StorageSize == "" {
			req.StorageSize = "200Gi"
		}
	}

	log.Printf("Auto-sizing: Workload type '%s' -> Storage size '%s'", req.WorkloadType, req.StorageSize)
}

// selectStorageClass selects appropriate storage class based on requirements
func (pc *ProvisioningController) selectStorageClass(req *types.ProvisioningRequest) string {
	// If performance requirements are specified, select based on them
	if req.RequiredReadThroughput > 0 || req.RequiredWriteThroughput > 0 || req.RequiredIOPS > 0 {
		// High throughput required
		if req.RequiredReadThroughput >= 600 || req.RequiredWriteThroughput >= 300 {
			return "high-throughput"
		}

		// High IOPS required
		if req.RequiredIOPS >= 8000 {
			return "high-iops"
		}

		// Balanced requirements
		return "balanced"
	}

	// Select based on workload type
	switch req.WorkloadType {
	case "training":
		// Training workloads benefit from high sequential throughput
		return "high-throughput"
	case "inference":
		// Inference benefits from balanced performance
		return "balanced"
	case "data-pipeline":
		// Data pipelines may need high IOPS for metadata operations
		return "high-iops"
	default:
		return "standard"
	}
}

// GetProvisioning retrieves the status of a provisioning job
func (pc *ProvisioningController) GetProvisioning(provisioningID string) (*types.ProvisioningResponse, error) {
	pc.provisioningsMux.RLock()
	job, exists := pc.provisionings[provisioningID]
	pc.provisioningsMux.RUnlock()

	if !exists {
		return nil, fmt.Errorf("provisioning %s not found", provisioningID)
	}

	return &types.ProvisioningResponse{
		ProvisioningID: job.ID,
		Status:         job.Status,
		Message:        pc.getStatusMessage(job.Status),
		Details:        job.Details,
	}, nil
}

// ListProvisionings returns all provisioning jobs
func (pc *ProvisioningController) ListProvisionings() []*types.ProvisioningResponse {
	pc.provisioningsMux.RLock()
	defer pc.provisioningsMux.RUnlock()

	result := make([]*types.ProvisioningResponse, 0, len(pc.provisionings))
	for _, job := range pc.provisionings {
		result = append(result, &types.ProvisioningResponse{
			ProvisioningID: job.ID,
			Status:         job.Status,
			Message:        pc.getStatusMessage(job.Status),
			Details:        job.Details,
		})
	}

	return result
}

// DeleteProvisioning deletes a provisioning job and its resources
func (pc *ProvisioningController) DeleteProvisioning(provisioningID string) error {
	pc.provisioningsMux.Lock()
	job, exists := pc.provisionings[provisioningID]
	if !exists {
		pc.provisioningsMux.Unlock()
		return fmt.Errorf("provisioning %s not found", provisioningID)
	}

	// Cancel the job context
	job.cancel()

	// Update status
	job.Status = types.ProvisioningStatusDeleting
	pc.provisioningsMux.Unlock()

	// TODO: Implement actual PVC deletion via Kubernetes API
	log.Printf("Provisioning %s: Deleting PVC %s", provisioningID, job.Details.PVCName)

	// Remove from map
	pc.provisioningsMux.Lock()
	delete(pc.provisionings, provisioningID)
	pc.metrics.ActiveProvisionings--
	pc.provisioningsMux.Unlock()

	log.Printf("Provisioning %s: Deleted successfully", provisioningID)
	return nil
}

// GetRecommendation provides storage recommendations for a workload
func (pc *ProvisioningController) GetRecommendation(workloadType string) *types.WorkloadStorageRecommendation {
	var recommendation types.WorkloadStorageRecommendation
	recommendation.WorkloadType = workloadType

	switch workloadType {
	case "training":
		recommendation.RecommendedSize = "500Gi"
		recommendation.RecommendedClass = "high-throughput"
		recommendation.RecommendedProfile = pc.storageProfiles["high-throughput"]
		recommendation.Reasoning = "AI training workloads require high sequential read throughput for streaming large datasets during training iterations"

	case "inference":
		recommendation.RecommendedSize = "100Gi"
		recommendation.RecommendedClass = "balanced"
		recommendation.RecommendedProfile = pc.storageProfiles["balanced"]
		recommendation.Reasoning = "Inference workloads need balanced performance for loading models and processing requests"

	case "data-pipeline":
		recommendation.RecommendedSize = "250Gi"
		recommendation.RecommendedClass = "high-iops"
		recommendation.RecommendedProfile = pc.storageProfiles["high-iops"]
		recommendation.Reasoning = "Data pipelines benefit from high IOPS for metadata operations and intermediate file processing"

	default:
		recommendation.RecommendedSize = "200Gi"
		recommendation.RecommendedClass = "standard"
		recommendation.RecommendedProfile = pc.storageProfiles["standard"]
		recommendation.Reasoning = "General purpose storage for unknown workload types"
	}

	return &recommendation
}

// GetMetrics returns provisioning metrics
func (pc *ProvisioningController) GetMetrics() *types.ProvisioningMetrics {
	pc.provisioningsMux.RLock()
	defer pc.provisioningsMux.RUnlock()

	return pc.metrics
}

// validateRequest validates a provisioning request
func (pc *ProvisioningController) validateRequest(req *types.ProvisioningRequest) error {
	if req.WorkloadName == "" {
		return fmt.Errorf("workload_name is required")
	}
	if req.WorkloadNamespace == "" {
		return fmt.Errorf("workload_namespace is required")
	}
	if req.WorkloadType == "" {
		return fmt.Errorf("workload_type is required")
	}

	// If auto_size is false, storage_size must be provided
	if !req.AutoSize && req.StorageSize == "" {
		return fmt.Errorf("storage_size is required when auto_size is false")
	}

	// Validate storage class if provided
	if req.StorageClass != "" {
		if _, exists := pc.storageProfiles[req.StorageClass]; !exists {
			return fmt.Errorf("invalid storage_class: %s (valid options: high-throughput, high-iops, balanced, standard)", req.StorageClass)
		}
	}

	return nil
}

// getStatusMessage returns a human-readable status message
func (pc *ProvisioningController) getStatusMessage(status types.ProvisioningStatus) string {
	switch status {
	case types.ProvisioningStatusPending:
		return "Provisioning request is pending"
	case types.ProvisioningStatusCreating:
		return "Creating storage resources"
	case types.ProvisioningStatusReady:
		return "Storage is ready and available"
	case types.ProvisioningStatusFailed:
		return "Provisioning failed"
	case types.ProvisioningStatusDeleting:
		return "Deleting storage resources"
	default:
		return "Unknown status"
	}
}
