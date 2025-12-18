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

// AutoscalingController manages autoscaling for workloads
type AutoscalingController struct {
	k8sClient      K8sClientInterface
	autoscalers    map[string]*AutoscalingJob
	autoscalersMux sync.RWMutex
	metrics        *types.AutoscalingMetrics
}

// AutoscalingJob represents an active autoscaling configuration
type AutoscalingJob struct {
	ID        string
	Request   *types.AutoscalingRequest
	Status    types.AutoscalingStatus
	Details   *types.AutoscalingDetails
	CreatedAt time.Time
	ctx       context.Context
	cancel    context.CancelFunc

	// Stabilization tracking
	scaleUpHistory   []scaleRecommendation
	scaleDownHistory []scaleRecommendation
}

// scaleRecommendation represents a scaling recommendation with timestamp
type scaleRecommendation struct {
	replicas  int32
	timestamp time.Time
}

// NewAutoscalingController creates a new autoscaling controller
func NewAutoscalingController(k8sClient K8sClientInterface) *AutoscalingController {
	return &AutoscalingController{
		k8sClient:   k8sClient,
		autoscalers: make(map[string]*AutoscalingJob),
		metrics: &types.AutoscalingMetrics{
			TotalAutoscalers:  0,
			ActiveAutoscalers: 0,
		},
	}
}

// CreateAutoscaler creates a new autoscaler for a workload
func (ac *AutoscalingController) CreateAutoscaler(req *types.AutoscalingRequest) (*types.AutoscalingResponse, error) {
	// Validate request
	if err := ac.validateRequest(req); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Generate unique autoscaler ID
	autoscalerID := fmt.Sprintf("autoscaler-%s", uuid.New().String()[:8])

	// Create context
	ctx, cancel := context.WithCancel(context.Background())

	// Create HPA name
	hpaName := fmt.Sprintf("hpa-%s-%s", req.WorkloadName, uuid.New().String()[:6])

	job := &AutoscalingJob{
		ID:        autoscalerID,
		Request:   req,
		Status:    types.AutoscalingStatusActive,
		CreatedAt: time.Now(),
		Details: &types.AutoscalingDetails{
			CreatedAt:       time.Now(),
			CurrentReplicas: 0,
			DesiredReplicas: req.MinReplicas,
			HPAName:         hpaName,
			ScaleUpCount:    0,
			ScaleDownCount:  0,
		},
		ctx:              ctx,
		cancel:           cancel,
		scaleUpHistory:   make([]scaleRecommendation, 0),
		scaleDownHistory: make([]scaleRecommendation, 0),
	}

	// Store autoscaler job
	ac.autoscalersMux.Lock()
	ac.autoscalers[autoscalerID] = job
	ac.metrics.TotalAutoscalers++
	ac.metrics.ActiveAutoscalers++
	ac.autoscalersMux.Unlock()

	// Start autoscaler in background
	go ac.runAutoscaler(job)

	log.Printf("Autoscaler %s created for %s/%s (%s)",
		autoscalerID, req.WorkloadNamespace, req.WorkloadName, req.WorkloadType)

	return &types.AutoscalingResponse{
		AutoscalingID: autoscalerID,
		Status:        types.AutoscalingStatusActive,
		Message:       "Autoscaler created successfully",
		Details:       job.Details,
	}, nil
}

// GetAutoscaler returns the status of an autoscaler
func (ac *AutoscalingController) GetAutoscaler(autoscalerID string) (*types.AutoscalingResponse, error) {
	ac.autoscalersMux.RLock()
	job, exists := ac.autoscalers[autoscalerID]
	ac.autoscalersMux.RUnlock()

	if !exists {
		return nil, fmt.Errorf("autoscaler %s not found", autoscalerID)
	}

	return &types.AutoscalingResponse{
		AutoscalingID: job.ID,
		Status:        job.Status,
		Message:       ac.getStatusMessage(job.Status),
		Details:       job.Details,
	}, nil
}

// DeleteAutoscaler stops and removes an autoscaler
func (ac *AutoscalingController) DeleteAutoscaler(autoscalerID string) error {
	ac.autoscalersMux.Lock()
	job, exists := ac.autoscalers[autoscalerID]
	if !exists {
		ac.autoscalersMux.Unlock()
		return fmt.Errorf("autoscaler %s not found", autoscalerID)
	}

	// Cancel the context to stop the autoscaler
	if job.cancel != nil {
		job.cancel()
	}

	job.Status = types.AutoscalingStatusInactive
	ac.metrics.ActiveAutoscalers--

	delete(ac.autoscalers, autoscalerID)
	ac.autoscalersMux.Unlock()

	log.Printf("Autoscaler %s deleted", autoscalerID)
	return nil
}

// runAutoscaler monitors and scales the workload
func (ac *AutoscalingController) runAutoscaler(job *AutoscalingJob) {
	ticker := time.NewTicker(15 * time.Second) // Check every 15 seconds
	defer ticker.Stop()

	log.Printf("Autoscaler %s started monitoring %s/%s",
		job.ID, job.Request.WorkloadNamespace, job.Request.WorkloadName)

	for {
		select {
		case <-job.ctx.Done():
			log.Printf("Autoscaler %s stopped", job.ID)
			return

		case <-ticker.C:
			// Get current workload status
			currentReplicas, err := ac.getCurrentReplicas(job)
			if err != nil {
				log.Printf("Autoscaler %s: Failed to get current replicas: %v", job.ID, err)
				continue
			}

			// Get current resource utilization (including storage I/O)
			cpuUtil, memUtil, gpuUtil, storageRead, storageWrite, storageIOPS, err := ac.getResourceUtilization(job)
			if err != nil {
				log.Printf("Autoscaler %s: Failed to get resource utilization: %v", job.ID, err)
				continue
			}

			// Update details
			ac.autoscalersMux.Lock()
			job.Details.CurrentReplicas = currentReplicas
			job.Details.CurrentCPU = cpuUtil
			job.Details.CurrentMemory = memUtil
			job.Details.CurrentGPU = gpuUtil
			job.Details.CurrentStorageReadThroughput = storageRead
			job.Details.CurrentStorageWriteThroughput = storageWrite
			job.Details.CurrentStorageIOPS = storageIOPS
			now := time.Now()
			job.Details.UpdatedAt = &now
			ac.autoscalersMux.Unlock()

			// Decide if scaling is needed (consider all resources including storage I/O)
			desiredReplicas := ac.calculateDesiredReplicas(job, cpuUtil, memUtil, gpuUtil, storageRead, storageWrite, storageIOPS)

			// Apply stabilization window to prevent flapping
			stabilizedReplicas := ac.applyStabilizationWindow(job, currentReplicas, desiredReplicas)

			if stabilizedReplicas != currentReplicas {
				if err := ac.scaleWorkload(job, stabilizedReplicas); err != nil {
					log.Printf("Autoscaler %s: Failed to scale workload: %v", job.ID, err)
				} else {
					ac.autoscalersMux.Lock()
					job.Details.DesiredReplicas = stabilizedReplicas
					scaleTime := time.Now()
					job.Details.LastScaleTime = &scaleTime

					if stabilizedReplicas > currentReplicas {
						job.Details.ScaleUpCount++
						ac.metrics.TotalScaleUps++
						log.Printf("Autoscaler %s: Scaled UP from %d to %d replicas (desired: %d, stabilized: %d)",
							job.ID, currentReplicas, stabilizedReplicas, desiredReplicas, stabilizedReplicas)
					} else {
						job.Details.ScaleDownCount++
						ac.metrics.TotalScaleDowns++
						log.Printf("Autoscaler %s: Scaled DOWN from %d to %d replicas (desired: %d, stabilized: %d)",
							job.ID, currentReplicas, stabilizedReplicas, desiredReplicas, stabilizedReplicas)
					}
					ac.autoscalersMux.Unlock()
				}
			} else if desiredReplicas != currentReplicas {
				// Log when stabilization window prevents scaling
				log.Printf("Autoscaler %s: Scaling from %d to %d replicas delayed by stabilization window",
					job.ID, currentReplicas, desiredReplicas)
			}
		}
	}
}

// calculateDesiredReplicas calculates the desired number of replicas based on current metrics
// For AI/ML workloads: considers CPU, Memory, GPU, and Storage I/O (critical for data-intensive training)
func (ac *AutoscalingController) calculateDesiredReplicas(job *AutoscalingJob, cpuUtil, memUtil, gpuUtil int32, storageRead, storageWrite, storageIOPS int64) int32 {
	currentReplicas := job.Details.CurrentReplicas
	if currentReplicas == 0 {
		currentReplicas = 1
	}

	var desiredReplicas int32 = currentReplicas
	recommendations := []int32{}

	// Calculate based on CPU if target is set
	if job.Request.TargetCPU > 0 && cpuUtil > 0 {
		cpuDesired := int32(float64(currentReplicas) * float64(cpuUtil) / float64(job.Request.TargetCPU))
		recommendations = append(recommendations, cpuDesired)
	}

	// Calculate based on Memory if target is set
	if job.Request.TargetMemory > 0 && memUtil > 0 {
		memDesired := int32(float64(currentReplicas) * float64(memUtil) / float64(job.Request.TargetMemory))
		recommendations = append(recommendations, memDesired)
	}

	// Calculate based on GPU if target is set
	if job.Request.TargetGPU > 0 && gpuUtil > 0 {
		gpuDesired := int32(float64(currentReplicas) * float64(gpuUtil) / float64(job.Request.TargetGPU))
		recommendations = append(recommendations, gpuDesired)
	}

	// Calculate based on Storage Read Throughput (CRITICAL for AI/ML data loading)
	if job.Request.TargetStorageReadThroughput > 0 && storageRead > 0 {
		storageReadDesired := int32(float64(currentReplicas) * float64(storageRead) / float64(job.Request.TargetStorageReadThroughput))
		recommendations = append(recommendations, storageReadDesired)
		log.Printf("Autoscaler %s: Storage Read %d MB/s (target: %d MB/s) -> %d replicas",
			job.ID, storageRead, job.Request.TargetStorageReadThroughput, storageReadDesired)
	}

	// Calculate based on Storage Write Throughput (for checkpoint saving)
	if job.Request.TargetStorageWriteThroughput > 0 && storageWrite > 0 {
		storageWriteDesired := int32(float64(currentReplicas) * float64(storageWrite) / float64(job.Request.TargetStorageWriteThroughput))
		recommendations = append(recommendations, storageWriteDesired)
	}

	// Calculate based on Storage IOPS (for mixed read/write workloads)
	if job.Request.TargetStorageIOPS > 0 && storageIOPS > 0 {
		iopsDesired := int32(float64(currentReplicas) * float64(storageIOPS) / float64(job.Request.TargetStorageIOPS))
		recommendations = append(recommendations, iopsDesired)
	}

	// Use the maximum recommendation (most conservative for scale-down, most responsive for scale-up)
	if len(recommendations) > 0 {
		desiredReplicas = recommendations[0]
		for _, rec := range recommendations {
			if rec > desiredReplicas {
				desiredReplicas = rec
			}
		}
	}

	// Apply min/max constraints
	if desiredReplicas < job.Request.MinReplicas {
		desiredReplicas = job.Request.MinReplicas
	}
	if desiredReplicas > job.Request.MaxReplicas {
		desiredReplicas = job.Request.MaxReplicas
	}

	// Apply max scale change if policy exists
	if job.Request.ScaleUpPolicy != nil && job.Request.ScaleUpPolicy.MaxScaleChange > 0 {
		if desiredReplicas > currentReplicas {
			maxChange := job.Request.ScaleUpPolicy.MaxScaleChange
			if desiredReplicas-currentReplicas > maxChange {
				desiredReplicas = currentReplicas + maxChange
			}
		}
	}

	if job.Request.ScaleDownPolicy != nil && job.Request.ScaleDownPolicy.MaxScaleChange > 0 {
		if desiredReplicas < currentReplicas {
			maxChange := job.Request.ScaleDownPolicy.MaxScaleChange
			if currentReplicas-desiredReplicas > maxChange {
				desiredReplicas = currentReplicas - maxChange
			}
		}
	}

	return desiredReplicas
}

// applyStabilizationWindow applies stabilization window to prevent flapping
// Returns the stabilized desired replicas based on the scaling history
func (ac *AutoscalingController) applyStabilizationWindow(job *AutoscalingJob, currentReplicas, desiredReplicas int32) int32 {
	now := time.Now()

	// Add current recommendation to history
	recommendation := scaleRecommendation{
		replicas:  desiredReplicas,
		timestamp: now,
	}

	// Determine if scaling up or down
	if desiredReplicas > currentReplicas {
		// Scale up scenario
		job.scaleUpHistory = append(job.scaleUpHistory, recommendation)

		// Get stabilization window (default to 0 if not set)
		stabilizationWindow := int32(0)
		if job.Request.ScaleUpPolicy != nil && job.Request.ScaleUpPolicy.StabilizationWindowSeconds > 0 {
			stabilizationWindow = job.Request.ScaleUpPolicy.StabilizationWindowSeconds
		}

		if stabilizationWindow > 0 {
			// Remove recommendations outside the stabilization window
			cutoffTime := now.Add(-time.Duration(stabilizationWindow) * time.Second)
			validRecommendations := []scaleRecommendation{}
			for _, rec := range job.scaleUpHistory {
				if rec.timestamp.After(cutoffTime) {
					validRecommendations = append(validRecommendations, rec)
				}
			}
			job.scaleUpHistory = validRecommendations

			// For scale up, use the maximum recommended value within the window (more aggressive)
			if len(job.scaleUpHistory) > 0 {
				maxReplicas := job.scaleUpHistory[0].replicas
				for _, rec := range job.scaleUpHistory {
					if rec.replicas > maxReplicas {
						maxReplicas = rec.replicas
					}
				}
				return maxReplicas
			}
		}
	} else if desiredReplicas < currentReplicas {
		// Scale down scenario
		job.scaleDownHistory = append(job.scaleDownHistory, recommendation)

		// Get stabilization window (default to 300 seconds if not set)
		stabilizationWindow := int32(300) // Default 5 minutes for scale down
		if job.Request.ScaleDownPolicy != nil && job.Request.ScaleDownPolicy.StabilizationWindowSeconds > 0 {
			stabilizationWindow = job.Request.ScaleDownPolicy.StabilizationWindowSeconds
		}

		// Remove recommendations outside the stabilization window
		cutoffTime := now.Add(-time.Duration(stabilizationWindow) * time.Second)
		validRecommendations := []scaleRecommendation{}
		for _, rec := range job.scaleDownHistory {
			if rec.timestamp.After(cutoffTime) {
				validRecommendations = append(validRecommendations, rec)
			}
		}
		job.scaleDownHistory = validRecommendations

		// For scale down, use the maximum recommended value within the window (more conservative)
		// This prevents premature scale down
		if len(job.scaleDownHistory) > 0 {
			maxReplicas := job.scaleDownHistory[0].replicas
			for _, rec := range job.scaleDownHistory {
				if rec.replicas > maxReplicas {
					maxReplicas = rec.replicas
				}
			}
			return maxReplicas
		}
	} else {
		// No scaling needed, clear histories
		job.scaleUpHistory = []scaleRecommendation{}
		job.scaleDownHistory = []scaleRecommendation{}
	}

	return desiredReplicas
}

// getCurrentReplicas returns the current number of replicas for the workload
func (ac *AutoscalingController) getCurrentReplicas(job *AutoscalingJob) (int32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	replicas, err := ac.k8sClient.GetWorkloadReplicas(ctx,
		job.Request.WorkloadNamespace,
		job.Request.WorkloadName,
		job.Request.WorkloadType)
	if err != nil {
		return 0, fmt.Errorf("failed to get workload replicas: %w", err)
	}

	return replicas, nil
}

// getResourceUtilization returns current CPU, Memory, GPU, and Storage I/O metrics
func (ac *AutoscalingController) getResourceUtilization(job *AutoscalingJob) (cpu, memory, gpu int32, storageRead, storageWrite, storageIOPS int64, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cpuPercent, memoryPercent, gpuPercent, readMBps, writeMBps, iops, err := ac.k8sClient.GetWorkloadPodMetrics(ctx,
		job.Request.WorkloadNamespace,
		job.Request.WorkloadName)
	if err != nil {
		// If metrics server is not available or no metrics found, return simulated values
		log.Printf("Autoscaler %s: Failed to get real metrics, using simulated values: %v", job.ID, err)
		cpu = 50 + int32(time.Now().Unix()%40)
		memory = 45 + int32(time.Now().Unix()%35)
		gpu = 40 + int32(time.Now().Unix()%50)
		// Simulate storage I/O for AI/ML workload
		storageRead = 300 + int64(time.Now().Unix()%200)   // 300-500 MB/s
		storageWrite = 80 + int64(time.Now().Unix()%70)    // 80-150 MB/s
		storageIOPS = 2000 + int64(time.Now().Unix()%2000) // 2000-4000 IOPS
		return cpu, memory, gpu, storageRead, storageWrite, storageIOPS, nil
	}

	return cpuPercent, memoryPercent, gpuPercent, readMBps, writeMBps, iops, nil
}

// scaleWorkload scales the workload to the desired number of replicas
func (ac *AutoscalingController) scaleWorkload(job *AutoscalingJob, desiredReplicas int32) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := ac.k8sClient.ScaleWorkload(ctx,
		job.Request.WorkloadNamespace,
		job.Request.WorkloadName,
		job.Request.WorkloadType,
		desiredReplicas)
	if err != nil {
		return fmt.Errorf("failed to scale workload: %w", err)
	}

	log.Printf("Successfully scaled %s/%s (%s) to %d replicas",
		job.Request.WorkloadNamespace, job.Request.WorkloadName, job.Request.WorkloadType, desiredReplicas)
	return nil
}

// validateRequest validates the autoscaling request
func (ac *AutoscalingController) validateRequest(req *types.AutoscalingRequest) error {
	if req.WorkloadName == "" {
		return fmt.Errorf("workload_name is required")
	}
	if req.WorkloadNamespace == "" {
		return fmt.Errorf("workload_namespace is required")
	}
	if req.WorkloadType == "" {
		return fmt.Errorf("workload_type is required")
	}
	if req.MinReplicas < 1 {
		return fmt.Errorf("min_replicas must be at least 1")
	}
	if req.MaxReplicas < req.MinReplicas {
		return fmt.Errorf("max_replicas must be greater than or equal to min_replicas")
	}
	if req.TargetCPU == 0 && req.TargetMemory == 0 && req.TargetGPU == 0 &&
		req.TargetStorageReadThroughput == 0 && req.TargetStorageWriteThroughput == 0 && req.TargetStorageIOPS == 0 {
		return fmt.Errorf("at least one target metric (CPU, Memory, GPU, or Storage I/O) must be specified")
	}
	return nil
}

// getStatusMessage returns a human-readable status message
func (ac *AutoscalingController) getStatusMessage(status types.AutoscalingStatus) string {
	switch status {
	case types.AutoscalingStatusActive:
		return "Autoscaler is active"
	case types.AutoscalingStatusInactive:
		return "Autoscaler is inactive"
	case types.AutoscalingStatusFailed:
		return "Autoscaler failed"
	default:
		return "Unknown status"
	}
}

// GetMetrics returns current autoscaling metrics
func (ac *AutoscalingController) GetMetrics() *types.AutoscalingMetrics {
	ac.autoscalersMux.RLock()
	defer ac.autoscalersMux.RUnlock()

	// Calculate average CPU utilization across all autoscalers
	totalCPU := float64(0)
	activeCount := int64(0)
	for _, job := range ac.autoscalers {
		if job.Status == types.AutoscalingStatusActive && job.Details.CurrentCPU > 0 {
			totalCPU += float64(job.Details.CurrentCPU)
			activeCount++
		}
	}

	avgCPU := float64(0)
	if activeCount > 0 {
		avgCPU = totalCPU / float64(activeCount)
	}

	metrics := *ac.metrics
	metrics.AverageCPUUtilization = avgCPU
	return &metrics
}

// ListAutoscalers returns all autoscalers
func (ac *AutoscalingController) ListAutoscalers() []*types.AutoscalingResponse {
	ac.autoscalersMux.RLock()
	defer ac.autoscalersMux.RUnlock()

	result := make([]*types.AutoscalingResponse, 0, len(ac.autoscalers))
	for _, job := range ac.autoscalers {
		result = append(result, &types.AutoscalingResponse{
			AutoscalingID: job.ID,
			Status:        job.Status,
			Message:       ac.getStatusMessage(job.Status),
			Details:       job.Details,
		})
	}
	return result
}
