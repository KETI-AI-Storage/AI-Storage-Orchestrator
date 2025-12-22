package controller

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-storage-orchestrator/pkg/types"

	"github.com/google/uuid"
)

// PreemptionController manages pod preemption operations
type PreemptionController struct {
	k8sClient K8sClientInterface
	jobs      map[string]*PreemptionJob
	jobsMux   sync.RWMutex
	metrics   *types.PreemptionMetrics
}

// PreemptionJob represents an active preemption job
type PreemptionJob struct {
	ID        string
	Request   *types.PreemptionRequest
	Status    types.PreemptionStatus
	Details   *types.PreemptionDetails
	CreatedAt time.Time
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewPreemptionController creates a new preemption controller
func NewPreemptionController(k8sClient K8sClientInterface) *PreemptionController {
	return &PreemptionController{
		k8sClient: k8sClient,
		jobs:      make(map[string]*PreemptionJob),
		metrics: &types.PreemptionMetrics{
			TotalPreemptionJobs:   0,
			ActivePreemptionJobs:  0,
			TotalPodsPreempted:    0,
			SuccessfulPreemptions: 0,
			FailedPreemptions:     0,
			TotalCPUFreed:         "0m",
			TotalMemoryFreed:      "0Mi",
			TotalGPUFreed:         0,
		},
	}
}

// StartPreemption initiates a new preemption operation
func (pc *PreemptionController) StartPreemption(req *types.PreemptionRequest) (*types.PreemptionResponse, error) {
	// Validate request
	if err := pc.validateRequest(req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Apply defaults
	pc.applyDefaults(req)

	// Create preemption job
	jobID := fmt.Sprintf("preempt-%s", uuid.New().String()[:8])
	ctx, cancel := context.WithCancel(context.Background())

	job := &PreemptionJob{
		ID:      jobID,
		Request: req,
		Status:  types.PreemptionStatusPending,
		Details: &types.PreemptionDetails{
			CreatedAt:            time.Now(),
			TargetResourceType:   req.ResourceType,
			TargetResourceAmount: req.TargetAmount,
			PreemptionCandidates: make([]types.PreemptionCandidate, 0),
			PreemptedPods:        make([]types.PreemptedPodInfo, 0),
			ResourceFreed:        types.ResourceAmount{},
		},
		CreatedAt: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Store job
	pc.jobsMux.Lock()
	pc.jobs[jobID] = job
	pc.metrics.TotalPreemptionJobs++
	pc.metrics.ActivePreemptionJobs++
	pc.jobsMux.Unlock()

	// Start preemption goroutine
	go pc.runPreemption(job)

	log.Printf("Preemption job %s started: node=%s, resource=%s, target=%s, strategy=%s",
		jobID, req.NodeName, req.ResourceType, req.TargetAmount, req.Strategy)

	return &types.PreemptionResponse{
		PreemptionID: jobID,
		Status:       job.Status,
		Message:      "Preemption job started",
		Details:      job.Details,
	}, nil
}

// validateRequest validates the preemption request
func (pc *PreemptionController) validateRequest(req *types.PreemptionRequest) error {
	if req.NodeName == "" {
		return fmt.Errorf("node_name is required")
	}

	if req.ResourceType == "" {
		return fmt.Errorf("resource_type is required")
	}

	validResourceTypes := []string{"cpu", "memory", "gpu", "storage", "storage_iops", "all"}
	isValidResource := false
	for _, rt := range validResourceTypes {
		if req.ResourceType == rt {
			isValidResource = true
			break
		}
	}
	if !isValidResource {
		return fmt.Errorf("invalid resource_type: %s (valid: cpu, memory, gpu, storage, all)", req.ResourceType)
	}

	if req.TargetAmount == "" {
		return fmt.Errorf("target_amount is required")
	}

	// Validate strategy if provided
	if req.Strategy != "" {
		validStrategies := []string{
			string(types.StrategyLowestPriority),
			string(types.StrategyYoungest),
			string(types.StrategyLargestResource),
			string(types.StrategyWeightedScore),
			string(types.StrategyStorageIOHeaviest),
			string(types.StrategyStorageAwareWeighted),
		}
		isValidStrategy := false
		for _, s := range validStrategies {
			if req.Strategy == s {
				isValidStrategy = true
				break
			}
		}
		if !isValidStrategy {
			return fmt.Errorf("invalid strategy: %s", req.Strategy)
		}
	}

	return nil
}

// applyDefaults applies default values to the request
func (pc *PreemptionController) applyDefaults(req *types.PreemptionRequest) {
	if req.Strategy == "" {
		req.Strategy = string(types.StrategyLowestPriority)
	}
	if req.MaxPodsToPreempt == 0 {
		req.MaxPodsToPreempt = 10
	}
	if req.GracePeriodSeconds == 0 {
		req.GracePeriodSeconds = 30
	}
	if req.ProtectedNamespaces == nil {
		req.ProtectedNamespaces = []string{"kube-system", "kube-public", "kube-node-lease"}
	}
}

// runPreemption executes the preemption workflow
func (pc *PreemptionController) runPreemption(job *PreemptionJob) {
	defer func() {
		pc.jobsMux.Lock()
		pc.metrics.ActivePreemptionJobs--
		now := time.Now()
		pc.metrics.LastPreemptionTime = &now
		pc.jobsMux.Unlock()
	}()

	// Phase 1: Analyze node state
	pc.updateJobStatus(job, types.PreemptionStatusAnalyzing)

	nodeState, err := pc.analyzeNodeState(job)
	if err != nil {
		pc.failJob(job, fmt.Sprintf("failed to analyze node state: %v", err))
		return
	}

	pc.jobsMux.Lock()
	job.Details.InitialNodeState = *nodeState
	pc.jobsMux.Unlock()

	// Phase 2: Find preemption candidates
	candidates, err := pc.findPreemptionCandidates(job, nodeState)
	if err != nil {
		pc.failJob(job, fmt.Sprintf("failed to find preemption candidates: %v", err))
		return
	}

	pc.jobsMux.Lock()
	job.Details.PreemptionCandidates = candidates
	job.Details.TotalPodsAnalyzed = int32(len(candidates))
	pc.jobsMux.Unlock()

	// Phase 3: Select pods to preempt based on strategy
	selectedPods := pc.selectPodsToPreempt(job, candidates)

	if len(selectedPods) == 0 {
		log.Printf("Preemption job %s: No suitable candidates found for preemption", job.ID)
		pc.jobsMux.Lock()
		job.Status = types.PreemptionStatusCompleted
		job.Details.PodsToPreempt = 0
		completedAt := time.Now()
		job.Details.CompletedAt = &completedAt
		pc.jobsMux.Unlock()
		return
	}

	pc.jobsMux.Lock()
	job.Details.PodsToPreempt = int32(len(selectedPods))
	pc.jobsMux.Unlock()

	// Phase 4: Execute preemption
	pc.updateJobStatus(job, types.PreemptionStatusExecuting)

	if err := pc.executePreemption(job, selectedPods); err != nil {
		pc.failJob(job, fmt.Sprintf("failed to execute preemption: %v", err))
		return
	}

	// Phase 5: Complete job
	pc.jobsMux.Lock()
	job.Status = types.PreemptionStatusCompleted
	completedAt := time.Now()
	job.Details.CompletedAt = &completedAt

	// Check if target was achieved
	job.Details.TargetAchieved = pc.checkTargetAchieved(job)
	pc.jobsMux.Unlock()

	log.Printf("Preemption job %s completed: %d pods preempted, target_achieved=%v",
		job.ID, job.Details.SuccessfulPreemptions, job.Details.TargetAchieved)
}

// analyzeNodeState analyzes the current resource state of the node
func (pc *PreemptionController) analyzeNodeState(job *PreemptionJob) (*types.NodeResourceState, error) {
	ctx := context.Background()
	nodeName := job.Request.NodeName

	// Get node metrics
	cpuPercent, memoryPercent, err := pc.k8sClient.GetNodeMetrics(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get node metrics: %w", err)
	}

	// Get node capacity
	cpuCapacity, memoryCapacity, gpuCapacity, err := pc.k8sClient.GetNodeCapacity(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get node capacity: %w", err)
	}

	// Get pod count
	podCount, err := pc.k8sClient.GetNodePodCount(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get pod count: %w", err)
	}

	// Get GPU utilization
	gpuPercent := int32(0)
	if gpuCapacity > 0 {
		gpuUtil, err := pc.k8sClient.GetNodeGPUUtilization(ctx, nodeName)
		if err == nil {
			gpuPercent = gpuUtil
		}
	}

	return &types.NodeResourceState{
		NodeName:          nodeName,
		CPUAllocatable:    cpuCapacity,
		MemoryAllocatable: memoryCapacity,
		GPUAllocatable:    gpuCapacity,
		CPUPercent:        cpuPercent,
		MemoryPercent:     memoryPercent,
		GPUPercent:        gpuPercent,
		PodCount:          podCount,
	}, nil
}

// findPreemptionCandidates finds pods that can be preempted
func (pc *PreemptionController) findPreemptionCandidates(job *PreemptionJob, nodeState *types.NodeResourceState) ([]types.PreemptionCandidate, error) {
	ctx := context.Background()
	candidates := make([]types.PreemptionCandidate, 0)

	// Get all pods on the node
	pods, err := pc.k8sClient.ListPodsOnNode(ctx, job.Request.NodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods on node: %w", err)
	}

	// Get pod details
	for _, pod := range pods {
		// Use pod namespace and name directly from PodRef
		podNamespace := pod.Namespace
		podName := pod.Name

		// Skip protected namespaces
		if pc.isProtectedNamespace(podNamespace, job.Request.ProtectedNamespaces) {
			continue
		}

		// Filter by requested namespace
		if job.Request.Namespace != "" && podNamespace != job.Request.Namespace {
			continue
		}

		// Get pod priority and resource info
		podInfo, err := pc.k8sClient.GetPodResourceInfo(ctx, podNamespace, podName)
		if err != nil {
			log.Printf("Warning: Failed to get pod info for %s/%s: %v", podNamespace, podName, err)
			continue
		}

		// Skip pods with priority >= MinPriority
		if podInfo.PriorityValue >= job.Request.MinPriority {
			continue
		}

		// Calculate age
		age := time.Since(podInfo.CreationTime)
		ageStr := formatDuration(age)

		// Calculate preemption score based on strategy
		score := pc.calculatePreemptionScore(job.Request.Strategy, podInfo)

		candidate := types.PreemptionCandidate{
			PodName:       podName,
			PodNamespace:  podNamespace,
			PriorityClass: podInfo.PriorityClass,
			PriorityValue: podInfo.PriorityValue,
			ResourceRequests: types.ResourceAmount{
				CPU:              formatMillicores(podInfo.CPURequest),
				Memory:           formatBytes(podInfo.MemoryRequest),
				GPU:              podInfo.GPURequest,
				StorageReadMBps:  podInfo.StorageReadMBps,
				StorageWriteMBps: podInfo.StorageWriteMBps,
				StorageIOPS:      podInfo.StorageIOPS,
			},
			CreationTime:     podInfo.CreationTime,
			Age:              ageStr,
			PreemptionScore:  score,
			PreemptionReason: pc.generatePreemptionReason(job.Request.Strategy, podInfo, job.Request.MinPriority),
			Selected:         false,
		}

		candidates = append(candidates, candidate)
	}

	// Sort candidates by preemption score (lower score = preempt first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].PreemptionScore < candidates[j].PreemptionScore
	})

	return candidates, nil
}

// generatePreemptionReason generates a human-readable reason for preemption based on strategy
func (pc *PreemptionController) generatePreemptionReason(strategy string, podInfo *types.PodResourceInfo, minPriority int32) string {
	switch types.PreemptionStrategy(strategy) {
	case types.StrategyLowestPriority:
		return fmt.Sprintf("Priority %d < MinPriority %d", podInfo.PriorityValue, minPriority)

	case types.StrategyYoungest:
		age := time.Since(podInfo.CreationTime)
		return fmt.Sprintf("Young pod (age: %s), Priority %d < %d", formatDuration(age), podInfo.PriorityValue, minPriority)

	case types.StrategyLargestResource:
		return fmt.Sprintf("Large resource consumer (CPU: %s, Mem: %s), Priority %d < %d",
			formatMillicores(podInfo.CPURequest), formatBytes(podInfo.MemoryRequest),
			podInfo.PriorityValue, minPriority)

	case types.StrategyWeightedScore:
		return fmt.Sprintf("Weighted score selection, Priority %d < %d", podInfo.PriorityValue, minPriority)

	case types.StrategyStorageIOHeaviest:
		return fmt.Sprintf("High storage I/O (Read: %dMB/s, Write: %dMB/s, IOPS: %d), Priority %d < %d",
			podInfo.StorageReadMBps, podInfo.StorageWriteMBps, podInfo.StorageIOPS,
			podInfo.PriorityValue, minPriority)

	case types.StrategyStorageAwareWeighted:
		return fmt.Sprintf("Storage-aware weighted (I/O: %dMB/s, CPU: %s, GPU: %d), Priority %d < %d",
			podInfo.StorageReadMBps+podInfo.StorageWriteMBps,
			formatMillicores(podInfo.CPURequest), podInfo.GPURequest,
			podInfo.PriorityValue, minPriority)

	default:
		return fmt.Sprintf("Priority %d < MinPriority %d", podInfo.PriorityValue, minPriority)
	}
}

// calculatePreemptionScore calculates a score for pod preemption (lower = preempt first)
func (pc *PreemptionController) calculatePreemptionScore(strategy string, podInfo *types.PodResourceInfo) float64 {
	switch types.PreemptionStrategy(strategy) {
	case types.StrategyLowestPriority:
		// Lower priority = lower score = preempt first
		return float64(podInfo.PriorityValue)

	case types.StrategyYoungest:
		// Younger pods = lower score = preempt first
		age := time.Since(podInfo.CreationTime)
		return age.Seconds()

	case types.StrategyLargestResource:
		// Larger resource = lower score = preempt first (negate to reverse)
		totalResource := float64(podInfo.CPURequest) + float64(podInfo.MemoryRequest)/1e9
		return -totalResource

	case types.StrategyWeightedScore:
		// Combined score: priority (40%), age (30%), resource (30%)
		priorityScore := float64(podInfo.PriorityValue) / 1000.0 // normalize
		ageScore := time.Since(podInfo.CreationTime).Hours() / 24.0 // normalize to days
		resourceScore := (float64(podInfo.CPURequest)/1000.0 + float64(podInfo.MemoryRequest)/1e9) / 10.0

		// Lower combined score = preempt first
		return 0.4*priorityScore + 0.3*(-ageScore) + 0.3*(-resourceScore)

	case types.StrategyStorageIOHeaviest:
		// Higher storage I/O = lower score = preempt first
		// This is useful when storage bandwidth is the bottleneck
		// Total I/O throughput in MB/s (read + write) + IOPS normalized
		totalThroughput := float64(podInfo.StorageReadMBps + podInfo.StorageWriteMBps)
		iopsNormalized := float64(podInfo.StorageIOPS) / 100.0 // normalize IOPS (divide by 100)

		// Negate to make higher I/O = lower score = preempt first
		return -(totalThroughput + iopsNormalized)

	case types.StrategyStorageAwareWeighted:
		// Combined score including storage I/O:
		// Priority (30%), Age (20%), Compute resources (25%), Storage I/O (25%)
		//
		// This strategy is designed for AI/ML workloads where both compute
		// and storage I/O are important factors for preemption decisions

		// Priority score (lower priority = lower score)
		priorityScore := float64(podInfo.PriorityValue) / 1000.0

		// Age score (younger = lower score, minimize work loss)
		ageScore := time.Since(podInfo.CreationTime).Hours() / 24.0

		// Compute resource score (larger = lower score)
		cpuNorm := float64(podInfo.CPURequest) / 4000.0      // normalize: 4 cores = 1.0
		memNorm := float64(podInfo.MemoryRequest) / 8e9      // normalize: 8GB = 1.0
		gpuNorm := float64(podInfo.GPURequest) / 2.0         // normalize: 2 GPUs = 1.0
		computeScore := cpuNorm + memNorm + gpuNorm*2.0      // GPU weighted more

		// Storage I/O score (higher I/O = lower score)
		readNorm := float64(podInfo.StorageReadMBps) / 500.0   // normalize: 500 MB/s = 1.0
		writeNorm := float64(podInfo.StorageWriteMBps) / 200.0 // normalize: 200 MB/s = 1.0
		iopsNorm := float64(podInfo.StorageIOPS) / 5000.0      // normalize: 5000 IOPS = 1.0
		storageScore := readNorm + writeNorm + iopsNorm

		// Combined weighted score (lower = preempt first)
		return 0.30*priorityScore + 0.20*(-ageScore) + 0.25*(-computeScore) + 0.25*(-storageScore)

	default:
		return float64(podInfo.PriorityValue)
	}
}

// selectPodsToPreempt selects which pods to preempt to meet the target
func (pc *PreemptionController) selectPodsToPreempt(job *PreemptionJob, candidates []types.PreemptionCandidate) []types.PreemptionCandidate {
	selectedPods := make([]types.PreemptionCandidate, 0)
	targetAmount := pc.parseResourceAmount(job.Request.TargetAmount, job.Request.ResourceType)
	accumulatedAmount := int64(0)

	for i := range candidates {
		if len(selectedPods) >= int(job.Request.MaxPodsToPreempt) {
			break
		}

		if accumulatedAmount >= targetAmount {
			break
		}

		candidate := &candidates[i]
		candidate.Selected = true

		// Accumulate resource based on type
		switch job.Request.ResourceType {
		case "cpu":
			accumulatedAmount += pc.parseCPU(candidate.ResourceRequests.CPU)
		case "memory":
			accumulatedAmount += pc.parseMemory(candidate.ResourceRequests.Memory)
		case "gpu":
			accumulatedAmount += int64(candidate.ResourceRequests.GPU)
		case "storage":
			// For storage, accumulate total I/O throughput (read + write MB/s)
			accumulatedAmount += candidate.ResourceRequests.StorageReadMBps + candidate.ResourceRequests.StorageWriteMBps
		case "storage_iops":
			// For storage IOPS specifically
			accumulatedAmount += candidate.ResourceRequests.StorageIOPS
		case "all":
			// For "all", we just count pods
			accumulatedAmount++
		}

		selectedPods = append(selectedPods, *candidate)
	}

	return selectedPods
}

// executePreemption executes the actual pod eviction
func (pc *PreemptionController) executePreemption(job *PreemptionJob, selectedPods []types.PreemptionCandidate) error {
	ctx := context.Background()
	results := make([]types.PreemptedPodInfo, 0)

	totalCPUFreed := int64(0)
	totalMemoryFreed := int64(0)
	totalGPUFreed := int32(0)
	totalStorageReadFreed := int64(0)
	totalStorageWriteFreed := int64(0)
	totalStorageIOPSFreed := int64(0)

	for _, pod := range selectedPods {
		preemptedAt := time.Now()

		// Evict the pod
		err := pc.k8sClient.EvictPod(ctx, pod.PodNamespace, pod.PodName, job.Request.GracePeriodSeconds)

		result := types.PreemptedPodInfo{
			PodName:       pod.PodName,
			PodNamespace:  pod.PodNamespace,
			PriorityValue: pod.PriorityValue,
			PreemptedAt:   preemptedAt,
			ResourceFreed: pod.ResourceRequests,
		}

		if err != nil {
			result.Status = "failed"
			result.ErrorMessage = err.Error()
			log.Printf("Failed to evict pod %s/%s: %v", pod.PodNamespace, pod.PodName, err)

			pc.jobsMux.Lock()
			job.Details.FailedPreemptions++
			pc.metrics.FailedPreemptions++
			pc.jobsMux.Unlock()
		} else {
			result.Status = "success"
			log.Printf("Successfully evicted pod %s/%s (Storage I/O freed: Read=%dMB/s, Write=%dMB/s, IOPS=%d)",
				pod.PodNamespace, pod.PodName,
				pod.ResourceRequests.StorageReadMBps,
				pod.ResourceRequests.StorageWriteMBps,
				pod.ResourceRequests.StorageIOPS)

			// Accumulate freed resources
			totalCPUFreed += pc.parseCPU(pod.ResourceRequests.CPU)
			totalMemoryFreed += pc.parseMemory(pod.ResourceRequests.Memory)
			totalGPUFreed += pod.ResourceRequests.GPU

			// Accumulate freed storage I/O
			totalStorageReadFreed += pod.ResourceRequests.StorageReadMBps
			totalStorageWriteFreed += pod.ResourceRequests.StorageWriteMBps
			totalStorageIOPSFreed += pod.ResourceRequests.StorageIOPS

			pc.jobsMux.Lock()
			job.Details.SuccessfulPreemptions++
			pc.metrics.SuccessfulPreemptions++
			pc.metrics.TotalPodsPreempted++
			pc.jobsMux.Unlock()
		}

		results = append(results, result)
	}

	// Update freed resources including Storage I/O
	pc.jobsMux.Lock()
	job.Details.PreemptedPods = results
	job.Details.ResourceFreed = types.ResourceAmount{
		CPU:              formatMillicores(totalCPUFreed),
		Memory:           formatBytes(totalMemoryFreed),
		GPU:              totalGPUFreed,
		StorageReadMBps:  totalStorageReadFreed,
		StorageWriteMBps: totalStorageWriteFreed,
		StorageIOPS:      totalStorageIOPSFreed,
	}

	// Update global metrics
	pc.updateGlobalMetrics(totalCPUFreed, totalMemoryFreed, totalGPUFreed)
	pc.jobsMux.Unlock()

	// Log storage I/O summary
	if totalStorageReadFreed > 0 || totalStorageWriteFreed > 0 || totalStorageIOPSFreed > 0 {
		log.Printf("Preemption job %s: Total Storage I/O freed - Read: %dMB/s, Write: %dMB/s, IOPS: %d",
			job.ID, totalStorageReadFreed, totalStorageWriteFreed, totalStorageIOPSFreed)
	}

	return nil
}

// checkTargetAchieved checks if the preemption target was met
func (pc *PreemptionController) checkTargetAchieved(job *PreemptionJob) bool {
	targetAmount := pc.parseResourceAmount(job.Request.TargetAmount, job.Request.ResourceType)
	freedAmount := int64(0)

	switch job.Request.ResourceType {
	case "cpu":
		freedAmount = pc.parseCPU(job.Details.ResourceFreed.CPU)
	case "memory":
		freedAmount = pc.parseMemory(job.Details.ResourceFreed.Memory)
	case "gpu":
		freedAmount = int64(job.Details.ResourceFreed.GPU)
	case "storage":
		// Total I/O throughput (read + write MB/s)
		freedAmount = job.Details.ResourceFreed.StorageReadMBps + job.Details.ResourceFreed.StorageWriteMBps
	case "storage_iops":
		freedAmount = job.Details.ResourceFreed.StorageIOPS
	case "all":
		freedAmount = int64(job.Details.SuccessfulPreemptions)
	}

	return freedAmount >= targetAmount
}

// Helper functions

func (pc *PreemptionController) updateJobStatus(job *PreemptionJob, status types.PreemptionStatus) {
	pc.jobsMux.Lock()
	defer pc.jobsMux.Unlock()

	job.Status = status
	now := time.Now()
	job.Details.UpdatedAt = &now
}

func (pc *PreemptionController) failJob(job *PreemptionJob, errorMsg string) {
	pc.jobsMux.Lock()
	defer pc.jobsMux.Unlock()

	job.Status = types.PreemptionStatusFailed
	job.Details.ErrorMessage = errorMsg
	completedAt := time.Now()
	job.Details.CompletedAt = &completedAt
	log.Printf("Preemption job %s failed: %s", job.ID, errorMsg)
}

func (pc *PreemptionController) parsePodFullName(fullName string) (namespace, name string) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "default", fullName
}

func (pc *PreemptionController) isProtectedNamespace(namespace string, protected []string) bool {
	for _, ns := range protected {
		if namespace == ns {
			return true
		}
	}
	return false
}

func (pc *PreemptionController) parseResourceAmount(amount string, resourceType string) int64 {
	switch resourceType {
	case "cpu":
		return pc.parseCPU(amount)
	case "memory":
		return pc.parseMemory(amount)
	case "gpu":
		val, _ := strconv.ParseInt(amount, 10, 64)
		return val
	case "all":
		val, _ := strconv.ParseInt(amount, 10, 64)
		return val
	default:
		return 0
	}
}

func (pc *PreemptionController) parseCPU(cpuStr string) int64 {
	cpuStr = strings.TrimSpace(cpuStr)
	if cpuStr == "" {
		return 0
	}

	// Handle millicores (e.g., "500m")
	if strings.HasSuffix(cpuStr, "m") {
		val, _ := strconv.ParseInt(strings.TrimSuffix(cpuStr, "m"), 10, 64)
		return val
	}

	// Handle cores (e.g., "2")
	val, _ := strconv.ParseFloat(cpuStr, 64)
	return int64(val * 1000)
}

func (pc *PreemptionController) parseMemory(memStr string) int64 {
	memStr = strings.TrimSpace(memStr)
	if memStr == "" {
		return 0
	}

	// Regular expression to parse memory values
	re := regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*([KMGTPE]i?)?[Bb]?$`)
	matches := re.FindStringSubmatch(memStr)

	if len(matches) < 2 {
		val, _ := strconv.ParseInt(memStr, 10, 64)
		return val
	}

	val, _ := strconv.ParseFloat(matches[1], 64)
	unit := ""
	if len(matches) >= 3 {
		unit = matches[2]
	}

	multipliers := map[string]float64{
		"":   1,
		"K":  1000,
		"Ki": 1024,
		"M":  1000 * 1000,
		"Mi": 1024 * 1024,
		"G":  1000 * 1000 * 1000,
		"Gi": 1024 * 1024 * 1024,
		"T":  1000 * 1000 * 1000 * 1000,
		"Ti": 1024 * 1024 * 1024 * 1024,
		"P":  1000 * 1000 * 1000 * 1000 * 1000,
		"Pi": 1024 * 1024 * 1024 * 1024 * 1024,
	}

	multiplier := multipliers[unit]
	return int64(val * multiplier)
}

func (pc *PreemptionController) updateGlobalMetrics(cpuFreed, memoryFreed int64, gpuFreed int32) {
	// Update cumulative metrics
	existingCPU := pc.parseCPU(pc.metrics.TotalCPUFreed)
	existingMemory := pc.parseMemory(pc.metrics.TotalMemoryFreed)

	pc.metrics.TotalCPUFreed = formatMillicores(existingCPU + cpuFreed)
	pc.metrics.TotalMemoryFreed = formatBytes(existingMemory + memoryFreed)
	pc.metrics.TotalGPUFreed += gpuFreed
}

// GetPreemption retrieves a preemption job by ID
func (pc *PreemptionController) GetPreemption(jobID string) (*types.PreemptionResponse, error) {
	pc.jobsMux.RLock()
	job, exists := pc.jobs[jobID]
	pc.jobsMux.RUnlock()

	if !exists {
		return nil, fmt.Errorf("preemption job not found: %s", jobID)
	}

	return &types.PreemptionResponse{
		PreemptionID: job.ID,
		Status:       job.Status,
		Message:      pc.getStatusMessage(job.Status),
		Details:      job.Details,
	}, nil
}

// ListPreemptions lists all preemption jobs
func (pc *PreemptionController) ListPreemptions() []*types.PreemptionResponse {
	pc.jobsMux.RLock()
	defer pc.jobsMux.RUnlock()

	result := make([]*types.PreemptionResponse, 0, len(pc.jobs))
	for _, job := range pc.jobs {
		result = append(result, &types.PreemptionResponse{
			PreemptionID: job.ID,
			Status:       job.Status,
			Message:      pc.getStatusMessage(job.Status),
			Details:      job.Details,
		})
	}
	return result
}

// GetMetrics returns preemption metrics
func (pc *PreemptionController) GetMetrics() *types.PreemptionMetrics {
	pc.jobsMux.RLock()
	defer pc.jobsMux.RUnlock()

	metrics := *pc.metrics
	return &metrics
}

// getStatusMessage returns a human-readable status message
func (pc *PreemptionController) getStatusMessage(status types.PreemptionStatus) string {
	switch status {
	case types.PreemptionStatusPending:
		return "Preemption job is pending"
	case types.PreemptionStatusAnalyzing:
		return "Analyzing node and pod state"
	case types.PreemptionStatusExecuting:
		return "Executing pod evictions"
	case types.PreemptionStatusCompleted:
		return "Preemption completed"
	case types.PreemptionStatusFailed:
		return "Preemption failed"
	default:
		return "Unknown status"
	}
}

// Helper functions for formatting
func formatMillicores(millicores int64) string {
	if millicores >= 1000 && millicores%1000 == 0 {
		return fmt.Sprintf("%d", millicores/1000)
	}
	return fmt.Sprintf("%dm", millicores)
}

func formatBytes(bytes int64) string {
	const (
		Ki = 1024
		Mi = Ki * 1024
		Gi = Mi * 1024
		Ti = Gi * 1024
	)

	switch {
	case bytes >= Ti:
		return fmt.Sprintf("%dTi", bytes/Ti)
	case bytes >= Gi:
		return fmt.Sprintf("%dGi", bytes/Gi)
	case bytes >= Mi:
		return fmt.Sprintf("%dMi", bytes/Mi)
	case bytes >= Ki:
		return fmt.Sprintf("%dKi", bytes/Ki)
	default:
		return fmt.Sprintf("%d", bytes)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
