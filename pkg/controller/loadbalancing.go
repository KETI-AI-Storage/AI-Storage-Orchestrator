package controller

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"sync"
	"time"

	"ai-storage-orchestrator/pkg/types"

	"github.com/google/uuid"
)

// LoadbalancingController manages loadbalancing operations
type LoadbalancingController struct {
	k8sClient          K8sClientInterface
	migrationController *MigrationController
	jobs               map[string]*LoadbalancingJob
	jobsMux            sync.RWMutex
	metrics            *types.LoadbalancingMetrics
}

// LoadbalancingJob represents an active loadbalancing job
type LoadbalancingJob struct {
	ID          string
	Request     *types.LoadbalancingRequest
	Status      types.LoadbalancingStatus
	Details     *types.LoadbalancingDetails
	CreatedAt   time.Time
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewLoadbalancingController creates a new loadbalancing controller
func NewLoadbalancingController(k8sClient K8sClientInterface, migrationController *MigrationController) *LoadbalancingController {
	return &LoadbalancingController{
		k8sClient:          k8sClient,
		migrationController: migrationController,
		jobs:               make(map[string]*LoadbalancingJob),
		metrics: &types.LoadbalancingMetrics{
			TotalLoadbalancingJobs:  0,
			ActiveLoadbalancingJobs: 0,
		},
	}
}

// StartLoadbalancing initiates a new loadbalancing job
func (lc *LoadbalancingController) StartLoadbalancing(req *types.LoadbalancingRequest) (string, error) {
	// Validate request
	if err := lc.validateRequest(req); err != nil {
		return "", fmt.Errorf("invalid request: %w", err)
	}

	// Create loadbalancing job
	jobID := fmt.Sprintf("lb-%s", uuid.New().String()[:8])
	ctx, cancel := context.WithCancel(context.Background())

	job := &LoadbalancingJob{
		ID:      jobID,
		Request: req,
		Status:  types.LoadbalancingStatusPending,
		Details: &types.LoadbalancingDetails{
			CreatedAt:         time.Now(),
			PlannedMigrations: make([]types.MigrationPlan, 0),
			ExecutedMigrations: make([]types.MigrationResult, 0),
		},
		CreatedAt: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Store job
	lc.jobsMux.Lock()
	lc.jobs[jobID] = job
	lc.metrics.TotalLoadbalancingJobs++
	lc.metrics.ActiveLoadbalancingJobs++
	lc.jobsMux.Unlock()

	// Start loadbalancing goroutine
	go lc.runLoadbalancing(job)

	log.Printf("Loadbalancing job %s started with strategy %s", jobID, req.Strategy)
	return jobID, nil
}

// validateRequest validates the loadbalancing request
func (lc *LoadbalancingController) validateRequest(req *types.LoadbalancingRequest) error {
	if req.Strategy == "" {
		req.Strategy = string(types.StrategyLoadSpreading)
	}

	// Validate strategy
	validStrategies := []string{
		string(types.StrategyLeastLoaded),
		string(types.StrategyLoadSpreading),
		string(types.StrategyStorageAware),
		string(types.StrategyWeighted),
		string(types.LBStrategyStorageIOBalanced),
		string(types.LBStrategyStorageAwareWeighted),
	}
	isValid := false
	for _, s := range validStrategies {
		if req.Strategy == s {
			isValid = true
			break
		}
	}
	if !isValid {
		return fmt.Errorf("invalid strategy: %s", req.Strategy)
	}

	// Set default thresholds
	if req.CPUThreshold == 0 {
		req.CPUThreshold = 80
	}
	if req.MemoryThreshold == 0 {
		req.MemoryThreshold = 80
	}
	if req.GPUThreshold == 0 {
		req.GPUThreshold = 80
	}
	if req.MaxMigrationsPerCycle == 0 {
		req.MaxMigrationsPerCycle = 5
	}

	// Set default Storage I/O thresholds for AI/ML workloads
	if req.StorageReadThreshold == 0 {
		req.StorageReadThreshold = 500 // 500 MB/s
	}
	if req.StorageWriteThreshold == 0 {
		req.StorageWriteThreshold = 200 // 200 MB/s
	}
	if req.StorageIOPSThreshold == 0 {
		req.StorageIOPSThreshold = 5000 // 5000 IOPS
	}

	return nil
}

// runLoadbalancing executes the loadbalancing workflow
func (lc *LoadbalancingController) runLoadbalancing(job *LoadbalancingJob) {
	defer func() {
		lc.jobsMux.Lock()
		lc.metrics.ActiveLoadbalancingJobs--
		now := time.Now()
		lc.metrics.LastLoadbalancingTime = &now
		lc.jobsMux.Unlock()
	}()

	// For periodic loadbalancing
	ticker := time.NewTicker(time.Duration(job.Request.Interval) * time.Second)
	defer ticker.Stop()

	for {
		// Execute one loadbalancing cycle
		if err := lc.executeCycle(job); err != nil {
			log.Printf("Loadbalancing job %s failed: %v", job.ID, err)
			lc.jobsMux.Lock()
			job.Status = types.LoadbalancingStatusFailed
			job.Details.ErrorMessage = err.Error()
			completedAt := time.Now()
			job.Details.CompletedAt = &completedAt
			lc.jobsMux.Unlock()
			return
		}

		// If not periodic, exit after one cycle
		if job.Request.Interval == 0 {
			lc.jobsMux.Lock()
			job.Status = types.LoadbalancingStatusCompleted
			completedAt := time.Now()
			job.Details.CompletedAt = &completedAt
			lc.jobsMux.Unlock()
			log.Printf("Loadbalancing job %s completed", job.ID)
			return
		}

		// Wait for next cycle or cancellation
		select {
		case <-job.ctx.Done():
			lc.jobsMux.Lock()
			job.Status = types.LoadbalancingStatusCancelled
			completedAt := time.Now()
			job.Details.CompletedAt = &completedAt
			lc.jobsMux.Unlock()
			log.Printf("Loadbalancing job %s cancelled", job.ID)
			return
		case <-ticker.C:
			// Continue to next cycle
		}
	}
}

// executeCycle executes one cycle of loadbalancing
func (lc *LoadbalancingController) executeCycle(job *LoadbalancingJob) error {
	// Phase 1: Analyze cluster state
	lc.jobsMux.Lock()
	job.Status = types.LoadbalancingStatusAnalyzing
	lc.jobsMux.Unlock()

	clusterState, err := lc.analyzeClusterState(job)
	if err != nil {
		return fmt.Errorf("failed to analyze cluster state: %w", err)
	}

	lc.jobsMux.Lock()
	job.Details.InitialState = *clusterState
	lc.jobsMux.Unlock()

	// Phase 2: Calculate migration plan
	migrationPlan, err := lc.calculateMigrationPlan(job, clusterState)
	if err != nil {
		return fmt.Errorf("failed to calculate migration plan: %w", err)
	}

	lc.jobsMux.Lock()
	job.Details.PlannedMigrations = migrationPlan
	job.Details.PodsToMigrate = int32(len(migrationPlan))
	lc.jobsMux.Unlock()

	// If no migrations needed, return success
	if len(migrationPlan) == 0 {
		log.Printf("Loadbalancing job %s: Cluster is already balanced", job.ID)
		return nil
	}

	// If dry-run mode, don't execute migrations
	if job.Request.DryRun {
		log.Printf("Loadbalancing job %s: Dry-run mode, %d migrations planned", job.ID, len(migrationPlan))
		return nil
	}

	// Phase 3: Execute migrations
	lc.jobsMux.Lock()
	job.Status = types.LoadbalancingStatusExecuting
	lc.jobsMux.Unlock()

	if err := lc.executeMigrations(job, migrationPlan); err != nil {
		return fmt.Errorf("failed to execute migrations: %w", err)
	}

	// Phase 4: Verify improvement
	finalState, err := lc.analyzeClusterState(job)
	if err != nil {
		log.Printf("Warning: Failed to analyze final cluster state: %v", err)
	} else {
		improvement := lc.calculateImprovement(&job.Details.InitialState, finalState)
		lc.jobsMux.Lock()
		job.Details.ResourceImprovement = improvement
		lc.metrics.AverageBalanceScore = finalState.BalanceScore
		lc.jobsMux.Unlock()
	}

	return nil
}

// analyzeClusterState analyzes the current resource utilization of the cluster
func (lc *LoadbalancingController) analyzeClusterState(job *LoadbalancingJob) (*types.ClusterState, error) {
	ctx := context.Background()

	// Get all nodes in cluster
	nodes, err := lc.k8sClient.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	clusterState := &types.ClusterState{
		Timestamp: time.Now(),
		Nodes:     make([]types.NodeState, 0),
	}

	// Filter nodes based on request
	targetNodesMap := make(map[string]bool)
	if len(job.Request.TargetNodes) > 0 {
		for _, nodeName := range job.Request.TargetNodes {
			targetNodesMap[nodeName] = true
		}
	}

	// Analyze each node
	for _, node := range nodes {
		// Skip if not in target nodes
		if len(targetNodesMap) > 0 && !targetNodesMap[node] {
			continue
		}

		nodeState, err := lc.getNodeState(ctx, node)
		if err != nil {
			log.Printf("Warning: Failed to get state for node %s: %v", node, err)
			continue
		}

		clusterState.Nodes = append(clusterState.Nodes, *nodeState)
		clusterState.TotalPods += nodeState.PodCount
	}

	// Calculate balance score
	clusterState.BalanceScore = lc.calculateBalanceScore(clusterState)

	return clusterState, nil
}

// getNodeState gets the resource state of a single node
func (lc *LoadbalancingController) getNodeState(ctx context.Context, nodeName string) (*types.NodeState, error) {
	// Get node metrics
	cpuPercent, memoryPercent, err := lc.k8sClient.GetNodeMetrics(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get node metrics: %w", err)
	}

	// Get node capacity
	cpuCapacity, memoryCapacity, gpuCapacity, err := lc.k8sClient.GetNodeCapacity(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get node capacity: %w", err)
	}

	// Get pod count on node
	podCount, err := lc.k8sClient.GetNodePodCount(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get pod count: %w", err)
	}

	// Get node layer label
	layer, err := lc.k8sClient.GetNodeLabel(ctx, nodeName, "layer")
	if err != nil {
		layer = ""
	}

	// Get GPU utilization for node
	gpuPercent := int32(0)
	if gpuCapacity > 0 {
		gpuUtil, err := lc.k8sClient.GetNodeGPUUtilization(ctx, nodeName)
		if err == nil {
			gpuPercent = gpuUtil
		}
	}

	// Get Storage I/O metrics for AI/ML workloads
	storageReadMBps := int64(0)
	storageWriteMBps := int64(0)
	storageIOPS := int64(0)
	storageUtilization := int32(0)
	readMBps, writeMBps, iops, util, err := lc.k8sClient.GetNodeStorageMetrics(ctx, nodeName)
	if err == nil {
		storageReadMBps = readMBps
		storageWriteMBps = writeMBps
		storageIOPS = iops
		storageUtilization = util
	}

	return &types.NodeState{
		NodeName:           nodeName,
		CPUPercent:         cpuPercent,
		MemoryPercent:      memoryPercent,
		GPUPercent:         gpuPercent,
		PodCount:           podCount,
		CPUCapacity:        cpuCapacity,
		MemoryCapacity:     memoryCapacity,
		GPUCapacity:        gpuCapacity,
		Layer:              layer,
		StorageReadMBps:    storageReadMBps,
		StorageWriteMBps:   storageWriteMBps,
		StorageIOPS:        storageIOPS,
		StorageUtilization: storageUtilization,
	}, nil
}

// calculateBalanceScore calculates a balance score (0-100) for the cluster
// Higher score means more balanced
func (lc *LoadbalancingController) calculateBalanceScore(state *types.ClusterState) float64 {
	if len(state.Nodes) == 0 {
		return 100.0
	}

	// Calculate coefficient of variation for each resource
	cpuCV := lc.calculateCoefficientOfVariation(state.Nodes, "cpu")
	memCV := lc.calculateCoefficientOfVariation(state.Nodes, "memory")
	gpuCV := lc.calculateCoefficientOfVariation(state.Nodes, "gpu")
	podCV := lc.calculateCoefficientOfVariation(state.Nodes, "pod")

	// Calculate Storage I/O coefficient of variation for AI/ML workloads
	storageReadCV := lc.calculateCoefficientOfVariation(state.Nodes, "storage_read")
	storageWriteCV := lc.calculateCoefficientOfVariation(state.Nodes, "storage_write")
	storageIOPSCV := lc.calculateCoefficientOfVariation(state.Nodes, "storage_iops")
	storageCV := (storageReadCV + storageWriteCV + storageIOPSCV) / 3.0

	// Lower CV means more balanced, convert to 0-100 score
	// CV of 0 = 100 score, CV of 1 = 0 score
	// Weight: CPU (20%), Memory (20%), GPU (15%), Pod (15%), Storage I/O (30%)
	avgCV := 0.20*cpuCV + 0.20*memCV + 0.15*gpuCV + 0.15*podCV + 0.30*storageCV
	score := math.Max(0, 100.0*(1.0-avgCV))

	return score
}

// calculateCoefficientOfVariation calculates the coefficient of variation for a resource
func (lc *LoadbalancingController) calculateCoefficientOfVariation(nodes []types.NodeState, resourceType string) float64 {
	if len(nodes) == 0 {
		return 0.0
	}

	values := make([]float64, 0, len(nodes))
	for _, node := range nodes {
		var val float64
		switch resourceType {
		case "cpu":
			val = float64(node.CPUPercent)
		case "memory":
			val = float64(node.MemoryPercent)
		case "gpu":
			val = float64(node.GPUPercent)
		case "pod":
			val = float64(node.PodCount)
		}
		values = append(values, val)
	}

	// Calculate mean
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))

	if mean == 0 {
		return 0.0
	}

	// Calculate standard deviation
	variance := 0.0
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(values))
	stdDev := math.Sqrt(variance)

	// Coefficient of variation = stdDev / mean
	return stdDev / mean
}

// calculateMigrationPlan calculates which pods should be migrated to which nodes
func (lc *LoadbalancingController) calculateMigrationPlan(job *LoadbalancingJob, state *types.ClusterState) ([]types.MigrationPlan, error) {
	strategy := types.LoadbalancingStrategy(job.Request.Strategy)

	switch strategy {
	case types.StrategyLeastLoaded:
		return lc.calculateLeastLoadedPlan(job, state)
	case types.StrategyLoadSpreading:
		return lc.calculateLoadSpreadingPlan(job, state)
	case types.StrategyStorageAware:
		return lc.calculateStorageAwarePlan(job, state)
	case types.StrategyWeighted:
		return lc.calculateWeightedPlan(job, state)
	default:
		return nil, fmt.Errorf("unsupported strategy: %s", strategy)
	}
}

// calculateLoadSpreadingPlan calculates a plan to spread load evenly across nodes
func (lc *LoadbalancingController) calculateLoadSpreadingPlan(job *LoadbalancingJob, state *types.ClusterState) ([]types.MigrationPlan, error) {
	ctx := context.Background()
	plan := make([]types.MigrationPlan, 0)

	// Sort nodes by load (highest first)
	sortedNodes := make([]types.NodeState, len(state.Nodes))
	copy(sortedNodes, state.Nodes)
	sort.Slice(sortedNodes, func(i, j int) bool {
		loadI := float64(sortedNodes[i].CPUPercent+sortedNodes[i].MemoryPercent) / 2.0
		loadJ := float64(sortedNodes[j].CPUPercent+sortedNodes[j].MemoryPercent) / 2.0
		return loadI > loadJ
	})

	// Identify overloaded nodes
	overloadedNodes := make([]types.NodeState, 0)
	underloadedNodes := make([]types.NodeState, 0)

	for _, node := range sortedNodes {
		avgLoad := float64(node.CPUPercent+node.MemoryPercent) / 2.0
		if avgLoad > float64(job.Request.CPUThreshold) {
			overloadedNodes = append(overloadedNodes, node)
		} else if avgLoad < 50.0 { // Nodes below 50% are considered underloaded
			underloadedNodes = append(underloadedNodes, node)
		}
	}

	// If no overloaded nodes or no underloaded nodes, no migration needed
	if len(overloadedNodes) == 0 || len(underloadedNodes) == 0 {
		return plan, nil
	}

	// For each overloaded node, find pods to migrate
	migrationsCount := 0
	for _, sourceNode := range overloadedNodes {
		if migrationsCount >= int(job.Request.MaxMigrationsPerCycle) {
			break
		}

		// Get pods on this node
		pods, err := lc.k8sClient.ListPodsOnNode(ctx, sourceNode.NodeName)
		if err != nil {
			log.Printf("Warning: Failed to list pods on node %s: %v", sourceNode.NodeName, err)
			continue
		}

		// Filter pods based on namespace
		filteredPods := make([]string, 0)
		for _, podName := range pods {
			namespace := job.Request.Namespace
			if namespace == "" {
				namespace = "default"
			}
			// TODO: Get actual pod namespace
			filteredPods = append(filteredPods, podName)
		}

		// Try to migrate some pods
		for _, podName := range filteredPods {
			if migrationsCount >= int(job.Request.MaxMigrationsPerCycle) {
				break
			}

			// Find best target node (least loaded)
			targetNode := underloadedNodes[0].NodeName

			plan = append(plan, types.MigrationPlan{
				PodName:      podName,
				PodNamespace: job.Request.Namespace,
				SourceNode:   sourceNode.NodeName,
				TargetNode:   targetNode,
				Reason:       fmt.Sprintf("Source node overloaded (%.1f%%), target node underloaded", float64(sourceNode.CPUPercent+sourceNode.MemoryPercent)/2.0),
				Priority:     int32(100 - migrationsCount),
			})

			migrationsCount++
		}
	}

	return plan, nil
}

// calculateLeastLoadedPlan moves pods to least loaded nodes
func (lc *LoadbalancingController) calculateLeastLoadedPlan(job *LoadbalancingJob, state *types.ClusterState) ([]types.MigrationPlan, error) {
	// Similar to load spreading but always targets the absolute least loaded node
	return lc.calculateLoadSpreadingPlan(job, state)
}

// calculateStorageAwarePlan prioritizes storage layer nodes
func (lc *LoadbalancingController) calculateStorageAwarePlan(job *LoadbalancingJob, state *types.ClusterState) ([]types.MigrationPlan, error) {
	// Filter for storage layer nodes
	storageNodes := make([]types.NodeState, 0)
	for _, node := range state.Nodes {
		if node.Layer == "storage" {
			storageNodes = append(storageNodes, node)
		}
	}

	if len(storageNodes) == 0 {
		log.Printf("Warning: No storage layer nodes found, falling back to load spreading")
		return lc.calculateLoadSpreadingPlan(job, state)
	}

	// Use load spreading on storage nodes only
	storageState := &types.ClusterState{
		Timestamp: state.Timestamp,
		Nodes:     storageNodes,
		TotalPods: 0,
	}
	for _, node := range storageNodes {
		storageState.TotalPods += node.PodCount
	}

	return lc.calculateLoadSpreadingPlan(job, storageState)
}

// calculateWeightedPlan uses weighted combination of all resources
func (lc *LoadbalancingController) calculateWeightedPlan(job *LoadbalancingJob, state *types.ClusterState) ([]types.MigrationPlan, error) {
	// TODO: Implement weighted strategy
	// For now, fall back to load spreading
	return lc.calculateLoadSpreadingPlan(job, state)
}

// executeMigrations executes the migration plan
func (lc *LoadbalancingController) executeMigrations(job *LoadbalancingJob, plan []types.MigrationPlan) error {
	results := make([]types.MigrationResult, 0)

	// Sort plan by priority
	sort.Slice(plan, func(i, j int) bool {
		return plan[i].Priority > plan[j].Priority
	})

	// Execute migrations
	for _, migration := range plan {
		startTime := time.Now()

		// Create migration request
		migReq := &types.MigrationRequest{
			PodName:      migration.PodName,
			PodNamespace: migration.PodNamespace,
			SourceNode:   migration.SourceNode,
			TargetNode:   migration.TargetNode,
			PreservePV:   job.Request.PreservePV,
			Timeout:      600, // 10 minutes
		}

		// Execute migration via migration controller
		migrationResp, err := lc.migrationController.StartMigration(migReq)

		endTime := time.Now()
		duration := endTime.Sub(startTime).Seconds()

		migrationID := ""
		if migrationResp != nil {
			migrationID = migrationResp.MigrationID
		}

		result := types.MigrationResult{
			MigrationID:  migrationID,
			PodName:      migration.PodName,
			PodNamespace: migration.PodNamespace,
			SourceNode:   migration.SourceNode,
			TargetNode:   migration.TargetNode,
			StartTime:    startTime,
			EndTime:      endTime,
			Duration:     duration,
		}

		if err != nil {
			result.Status = "failed"
			result.ErrorMessage = err.Error()
			log.Printf("Migration failed: %s/%s from %s to %s: %v",
				migration.PodNamespace, migration.PodName,
				migration.SourceNode, migration.TargetNode, err)

			lc.jobsMux.Lock()
			job.Details.FailedMigrations++
			lc.metrics.FailedMigrations++
			lc.jobsMux.Unlock()
		} else {
			result.Status = "success"
			log.Printf("Migration succeeded: %s/%s from %s to %s",
				migration.PodNamespace, migration.PodName,
				migration.SourceNode, migration.TargetNode)

			lc.jobsMux.Lock()
			job.Details.SuccessfulMigrations++
			lc.metrics.SuccessfulMigrations++
			lc.metrics.TotalMigrationsExecuted++
			lc.jobsMux.Unlock()
		}

		results = append(results, result)
	}

	lc.jobsMux.Lock()
	job.Details.ExecutedMigrations = results
	lc.jobsMux.Unlock()

	return nil
}

// calculateImprovement calculates the improvement in resource utilization
func (lc *LoadbalancingController) calculateImprovement(before, after *types.ClusterState) *types.ResourceImprovement {
	return &types.ResourceImprovement{
		CPUVarianceBefore:       lc.calculateCoefficientOfVariation(before.Nodes, "cpu"),
		CPUVarianceAfter:        lc.calculateCoefficientOfVariation(after.Nodes, "cpu"),
		MemoryVarianceBefore:    lc.calculateCoefficientOfVariation(before.Nodes, "memory"),
		MemoryVarianceAfter:     lc.calculateCoefficientOfVariation(after.Nodes, "memory"),
		GPUVarianceBefore:       lc.calculateCoefficientOfVariation(before.Nodes, "gpu"),
		GPUVarianceAfter:        lc.calculateCoefficientOfVariation(after.Nodes, "gpu"),
		BalanceScoreImprovement: after.BalanceScore - before.BalanceScore,
	}
}

// GetLoadbalancingJob retrieves a loadbalancing job by ID
func (lc *LoadbalancingController) GetLoadbalancingJob(jobID string) (*types.LoadbalancingResponse, error) {
	lc.jobsMux.RLock()
	job, exists := lc.jobs[jobID]
	lc.jobsMux.RUnlock()

	if !exists {
		return nil, fmt.Errorf("loadbalancing job not found: %s", jobID)
	}

	return &types.LoadbalancingResponse{
		LoadbalancingID: job.ID,
		Status:          job.Status,
		Message:         lc.getStatusMessage(job.Status),
		Details:         job.Details,
	}, nil
}

// ListLoadbalancingJobs lists all loadbalancing jobs
func (lc *LoadbalancingController) ListLoadbalancingJobs() []*types.LoadbalancingResponse {
	lc.jobsMux.RLock()
	defer lc.jobsMux.RUnlock()

	result := make([]*types.LoadbalancingResponse, 0, len(lc.jobs))
	for _, job := range lc.jobs {
		result = append(result, &types.LoadbalancingResponse{
			LoadbalancingID: job.ID,
			Status:          job.Status,
			Message:         lc.getStatusMessage(job.Status),
			Details:         job.Details,
		})
	}
	return result
}

// CancelLoadbalancing cancels a running loadbalancing job
func (lc *LoadbalancingController) CancelLoadbalancing(jobID string) error {
	lc.jobsMux.RLock()
	job, exists := lc.jobs[jobID]
	lc.jobsMux.RUnlock()

	if !exists {
		return fmt.Errorf("loadbalancing job not found: %s", jobID)
	}

	if job.Status == types.LoadbalancingStatusCompleted ||
		job.Status == types.LoadbalancingStatusFailed ||
		job.Status == types.LoadbalancingStatusCancelled {
		return fmt.Errorf("cannot cancel loadbalancing job in status: %s", job.Status)
	}

	job.cancel()
	log.Printf("Loadbalancing job %s cancelled", jobID)
	return nil
}

// GetMetrics returns loadbalancing metrics
func (lc *LoadbalancingController) GetMetrics() *types.LoadbalancingMetrics {
	lc.jobsMux.RLock()
	defer lc.jobsMux.RUnlock()

	// Create a copy to avoid race conditions
	metrics := *lc.metrics
	return &metrics
}

// getStatusMessage returns a human-readable status message
func (lc *LoadbalancingController) getStatusMessage(status types.LoadbalancingStatus) string {
	switch status {
	case types.LoadbalancingStatusPending:
		return "Loadbalancing job is pending"
	case types.LoadbalancingStatusAnalyzing:
		return "Analyzing cluster state"
	case types.LoadbalancingStatusExecuting:
		return "Executing pod migrations"
	case types.LoadbalancingStatusCompleted:
		return "Loadbalancing completed successfully"
	case types.LoadbalancingStatusFailed:
		return "Loadbalancing failed"
	case types.LoadbalancingStatusCancelled:
		return "Loadbalancing cancelled"
	default:
		return "Unknown status"
	}
}
