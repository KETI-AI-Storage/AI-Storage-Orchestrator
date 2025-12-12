package controller

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"ai-storage-orchestrator/pkg/k8s"
	"ai-storage-orchestrator/pkg/types"

	"github.com/google/uuid"
)

// AutoscalingController manages autoscaling for workloads
type AutoscalingController struct {
	k8sClient      *k8s.Client
	autoscalers    map[string]*AutoscalingJob
	autoscalersMux sync.RWMutex
	metrics        *types.AutoscalingMetrics
}

// AutoscalingJob represents an active autoscaling configuration
type AutoscalingJob struct {
	ID          string
	Request     *types.AutoscalingRequest
	Status      types.AutoscalingStatus
	Details     *types.AutoscalingDetails
	CreatedAt   time.Time
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewAutoscalingController creates a new autoscaling controller
func NewAutoscalingController(k8sClient *k8s.Client) *AutoscalingController {
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
		ctx:    ctx,
		cancel: cancel,
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

			// Get current resource utilization
			cpuUtil, memUtil, gpuUtil, err := ac.getResourceUtilization(job)
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
			now := time.Now()
			job.Details.UpdatedAt = &now
			ac.autoscalersMux.Unlock()

			// Decide if scaling is needed
			desiredReplicas := ac.calculateDesiredReplicas(job, cpuUtil, memUtil, gpuUtil)

			if desiredReplicas != currentReplicas {
				if err := ac.scaleWorkload(job, desiredReplicas); err != nil {
					log.Printf("Autoscaler %s: Failed to scale workload: %v", job.ID, err)
				} else {
					ac.autoscalersMux.Lock()
					job.Details.DesiredReplicas = desiredReplicas
					scaleTime := time.Now()
					job.Details.LastScaleTime = &scaleTime

					if desiredReplicas > currentReplicas {
						job.Details.ScaleUpCount++
						ac.metrics.TotalScaleUps++
						log.Printf("Autoscaler %s: Scaled UP from %d to %d replicas",
							job.ID, currentReplicas, desiredReplicas)
					} else {
						job.Details.ScaleDownCount++
						ac.metrics.TotalScaleDowns++
						log.Printf("Autoscaler %s: Scaled DOWN from %d to %d replicas",
							job.ID, currentReplicas, desiredReplicas)
					}
					ac.autoscalersMux.Unlock()
				}
			}
		}
	}
}

// calculateDesiredReplicas calculates the desired number of replicas based on current metrics
func (ac *AutoscalingController) calculateDesiredReplicas(job *AutoscalingJob, cpuUtil, memUtil, gpuUtil int32) int32 {
	currentReplicas := job.Details.CurrentReplicas
	if currentReplicas == 0 {
		currentReplicas = 1
	}

	var desiredReplicas int32 = currentReplicas

	// Calculate based on CPU if target is set
	if job.Request.TargetCPU > 0 && cpuUtil > 0 {
		cpuDesired := int32(float64(currentReplicas) * float64(cpuUtil) / float64(job.Request.TargetCPU))
		if cpuDesired > desiredReplicas {
			desiredReplicas = cpuDesired
		}
	}

	// Calculate based on Memory if target is set
	if job.Request.TargetMemory > 0 && memUtil > 0 {
		memDesired := int32(float64(currentReplicas) * float64(memUtil) / float64(job.Request.TargetMemory))
		if memDesired > desiredReplicas {
			desiredReplicas = memDesired
		}
	}

	// Calculate based on GPU if target is set
	if job.Request.TargetGPU > 0 && gpuUtil > 0 {
		gpuDesired := int32(float64(currentReplicas) * float64(gpuUtil) / float64(job.Request.TargetGPU))
		if gpuDesired > desiredReplicas {
			desiredReplicas = gpuDesired
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

// getCurrentReplicas returns the current number of replicas for the workload
func (ac *AutoscalingController) getCurrentReplicas(job *AutoscalingJob) (int32, error) {
	// This is a simplified implementation - in production, would query actual K8s resources
	// For now, return the desired replicas from details or min replicas
	if job.Details.CurrentReplicas > 0 {
		return job.Details.CurrentReplicas, nil
	}
	return job.Request.MinReplicas, nil
}

// getResourceUtilization returns current CPU, Memory, and GPU utilization percentages
func (ac *AutoscalingController) getResourceUtilization(job *AutoscalingJob) (cpu, memory, gpu int32, err error) {
	// This is a simplified implementation - in production, would query metrics server
	// For demonstration, return simulated values between 40-90%
	cpu = 50 + int32(time.Now().Unix()%40)
	memory = 45 + int32(time.Now().Unix()%35)
	gpu = 40 + int32(time.Now().Unix()%50)
	return cpu, memory, gpu, nil
}

// scaleWorkload scales the workload to the desired number of replicas
func (ac *AutoscalingController) scaleWorkload(job *AutoscalingJob, desiredReplicas int32) error {
	// This is a simplified implementation - in production, would use K8s scale API
	// For demonstration, just log the scaling action
	log.Printf("Scaling %s/%s to %d replicas",
		job.Request.WorkloadNamespace, job.Request.WorkloadName, desiredReplicas)
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
	if req.TargetCPU == 0 && req.TargetMemory == 0 && req.TargetGPU == 0 {
		return fmt.Errorf("at least one target metric (CPU, Memory, or GPU) must be specified")
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
