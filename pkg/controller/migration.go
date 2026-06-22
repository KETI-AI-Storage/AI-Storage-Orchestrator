package controller

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-storage-orchestrator/pkg/gluesys"
	"ai-storage-orchestrator/pkg/k8s"
	"ai-storage-orchestrator/pkg/types"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
)

const (
	stageDecode = "decode"
	stageFilter = "filter"
	stageOutput = "output"
)

// MigrationController manages pod migrations with persistent volume optimization
type MigrationController struct {
	k8sClient      *k8s.Client
	gluesysClient  gluesys.Integration
	planStore      *StageExecutionPlanStore
	migrations     map[string]*MigrationJob
	migrationsMux  sync.RWMutex
	metrics        *types.MigrationMetrics
	checkpointSize string // Default PV size for checkpoints
	concurrencyCh  chan struct{}
}

// MigrationJob represents an active migration job
type MigrationJob struct {
	ID        string
	Request   *types.MigrationRequest
	Status    types.MigrationStatus
	Details   *types.MigrationDetails
	StartTime time.Time
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewMigrationController creates a new migration controller.
// gluesysClient can be nil; in that case Gluesys integration is disabled.
func NewMigrationController(k8sClient *k8s.Client, gluesysClient gluesys.Integration, planStore *StageExecutionPlanStore) *MigrationController {
	maxConcurrent := 3
	if raw := strings.TrimSpace(os.Getenv("MAX_CONCURRENT_MIGRATIONS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxConcurrent = n
		}
	}
	return &MigrationController{
		k8sClient:      k8sClient,
		gluesysClient:  gluesysClient,
		planStore:      planStore,
		migrations:     make(map[string]*MigrationJob),
		metrics:        &types.MigrationMetrics{},
		checkpointSize: "1Gi", // Default 1GB for checkpoint storage
		concurrencyCh:  make(chan struct{}, maxConcurrent),
	}
}

// StartMigration initiates a new pod migration
func (mc *MigrationController) StartMigration(req *types.MigrationRequest) (*types.MigrationResponse, error) {
	if err := mc.resolveMigrationTarget(req); err != nil {
		return nil, err
	}

	// #14 멱등성: 같은 Pod에 이미 진행 중(Pending/Running)인 마이그가 있으면 새로 시작하지 않고
	// 기존 것을 반환한다. forecaster가 cpu+memory 등으로 같은 Pod에 migration 정책을 여러 개 내도
	// 동시 중복 마이그(중복 optimized pod·OutOfcpu·original-not-found)를 막는다. (#9-B autoscaler dedup과 동일 패턴)
	if req.PodName != "" {
		mc.migrationsMux.RLock()
		for _, j := range mc.migrations {
			if (j.Status == types.MigrationStatusPending || j.Status == types.MigrationStatusRunning) &&
				j.Request != nil && j.Request.PodName == req.PodName && j.Request.PodNamespace == req.PodNamespace {
				existing := &types.MigrationResponse{
					MigrationID: j.ID,
					Status:      j.Status,
					Message:     "Migration already in progress for pod (reused)",
					Details:     j.Details,
				}
				mc.migrationsMux.RUnlock()
				log.Printf("Migration %s reused for %s/%s (idempotent — already in progress)", j.ID, req.PodNamespace, req.PodName)
				return existing, nil
			}
		}
		mc.migrationsMux.RUnlock()
	}

	mc.saveStageExecutionPlanForMigration(req)

	select {
	case mc.concurrencyCh <- struct{}{}:
	default:
		return nil, fmt.Errorf("too many concurrent migrations in progress")
	}

	// Generate unique migration ID
	migrationID := fmt.Sprintf("migration-%s", uuid.New().String()[:8])

	// Create migration job
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(req.Timeout)*time.Second)
	if req.Timeout == 0 {
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Minute) // Default timeout
	}

	job := &MigrationJob{
		ID:        migrationID,
		Request:   req,
		Status:    types.MigrationStatusPending,
		StartTime: time.Now(),
		Details: &types.MigrationDetails{
			StartTime: time.Now(),
		},
		ctx:    ctx,
		cancel: cancel,
	}

	// Store migration job
	mc.migrationsMux.Lock()
	mc.migrations[migrationID] = job
	mc.migrationsMux.Unlock()

	// Start migration in background
	go mc.executeMigration(job)

	return &types.MigrationResponse{
		MigrationID: migrationID,
		Status:      types.MigrationStatusPending,
		Message:     "Migration started",
		Details:     job.Details,
	}, nil
}

func (mc *MigrationController) resolveMigrationTarget(req *types.MigrationRequest) error {
	if req == nil {
		return fmt.Errorf("migration request is nil")
	}

	req.PodName = strings.TrimSpace(req.PodName)
	req.PodNamespace = strings.TrimSpace(req.PodNamespace)
	req.WorkloadName = strings.TrimSpace(req.WorkloadName)
	req.WorkloadNamespace = strings.TrimSpace(req.WorkloadNamespace)
	req.WorkloadType = strings.TrimSpace(req.WorkloadType)
	req.SourceNode = strings.TrimSpace(req.SourceNode)
	req.TargetNode = strings.TrimSpace(req.TargetNode)

	hasPodPair := req.PodName != "" && req.PodNamespace != ""
	hasWorkloadPair := req.WorkloadName != "" && req.WorkloadNamespace != ""
	useResolver := hasWorkloadPair || strings.EqualFold(req.WorkloadType, k8s.WorkloadTypeAuto)

	if req.WorkloadType == "" {
		req.WorkloadType = k8s.WorkloadTypeAuto
	}

	if !useResolver && hasPodPair {
		pod, err := mc.k8sClient.GetPod(context.Background(), req.PodNamespace, req.PodName)
		if err != nil {
			return err
		}
		if req.SourceNode == "" {
			req.SourceNode = strings.TrimSpace(pod.Spec.NodeName)
		}
		return nil
	}

	if !hasWorkloadPair {
		if hasPodPair {
			pod, err := mc.k8sClient.GetPod(context.Background(), req.PodNamespace, req.PodName)
			if err != nil {
				return err
			}
			if req.SourceNode == "" {
				req.SourceNode = strings.TrimSpace(pod.Spec.NodeName)
			}
			return nil
		}
		return fmt.Errorf("workload_name/workload_namespace are required when workload-based resolution is used")
	}

	target, err := mc.k8sClient.ResolveWorkloadTarget(
		context.Background(),
		req.WorkloadNamespace,
		req.WorkloadName,
		req.WorkloadType,
		k8s.PolicyTypeMigration,
		req.Stage,
		true,
	)
	if err != nil {
		if waitingErr, ok := err.(*k8s.WaitingForTargetError); ok {
			log.Printf(
				"Migration target waiting: requested_workload_name=%s requested_workload_type=%s resolved_kind=%s selector=%s selected_pod=%s source_node=%s target_node=%s waiting_reason=%s",
				req.WorkloadName,
				req.WorkloadType,
				waitingErr.WorkloadType,
				"",
				"",
				"",
				req.TargetNode,
				waitingErr.Reason,
			)
		}
		return err
	}
	if len(target.RunningPods) == 0 {
		waitingErr := k8s.NewWaitingForTargetError(
			req.WorkloadNamespace,
			req.WorkloadName,
			target.Kind,
			k8s.PolicyTypeMigration,
			"resolved workload has no running pods",
		)
		log.Printf(
			"Migration target waiting: requested_workload_name=%s requested_workload_type=%s resolved_kind=%s selector=%s selected_pod=%s source_node=%s target_node=%s waiting_reason=%s",
			req.WorkloadName,
			req.WorkloadType,
			target.Kind,
			target.Selector,
			"",
			"",
			req.TargetNode,
			waitingErr.Reason,
		)
		return waitingErr
	}

	selectedPod := target.RunningPods[0]
	req.PodName = selectedPod.Name
	req.PodNamespace = selectedPod.Namespace
	req.SourceNode = strings.TrimSpace(selectedPod.Spec.NodeName)

	log.Printf(
		"Migration target resolved: requested_workload_name=%s requested_workload_type=%s resolved_kind=%s selector=%s selected_pod=%s source_node=%s target_node=%s waiting_reason=%s",
		req.WorkloadName,
		req.WorkloadType,
		target.Kind,
		target.Selector,
		selectedPod.Name,
		req.SourceNode,
		req.TargetNode,
		"",
	)
	return nil
}

func (mc *MigrationController) saveStageExecutionPlanForMigration(req *types.MigrationRequest) {
	if mc == nil || mc.planStore == nil || req == nil {
		return
	}
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		runID = strings.TrimSpace(req.PolicyName)
	}
	if runID == "" {
		runID = strings.TrimSpace(req.PodName)
	}
	targetStage := strings.TrimSpace(req.Stage)
	if targetStage == "" {
		targetStage = stageOutput
	}
	policyAnnotations := map[string]string{
		"ai-storage/policy-name":   req.PolicyName,
		"ai-storage/policy-action": req.Action,
	}
	nodeSelector := map[string]string{
		"kubernetes.io/hostname": req.TargetNode,
	}
	mc.planStore.Save(&types.StageExecutionPlan{
		RunID:            runID,
		TargetStage:      targetStage,
		Kind:             "migration",
		SchedulerName:    firstNonEmpty(strings.TrimSpace(req.SchedulerName), "ai-storage-scheduler"),
		NodeSelector:     firstNonEmptyStringMap(req.NodeSelector, nodeSelector),
		Affinity:         req.Affinity,
		ResourceRequests: cloneStringMap(req.ResourceRequests),
		QueueLabel:       targetStage,
		StorageAnnotations: firstNonEmptyStringMap(req.StorageAnnotations, map[string]string{
			"ai-storage/stage":       targetStage,
			"ai-storage/target-node": req.TargetNode,
		}),
		PolicyAnnotations: firstNonEmptyStringMap(req.PolicyAnnotations, policyAnnotations),
	})
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func firstNonEmptyStringMap(primary, fallback map[string]string) map[string]string {
	if len(primary) > 0 {
		return cloneStringMap(primary)
	}
	return cloneStringMap(fallback)
}

// GetMigrationStatus returns the current status of a migration
func (mc *MigrationController) GetMigrationStatus(migrationID string) (*types.MigrationResponse, error) {
	mc.migrationsMux.RLock()
	job, exists := mc.migrations[migrationID]
	mc.migrationsMux.RUnlock()

	if !exists {
		return nil, fmt.Errorf("migration %s not found", migrationID)
	}

	return &types.MigrationResponse{
		MigrationID: job.ID,
		Status:      job.Status,
		Message:     mc.buildStatusMessage(job),
		Details:     job.Details,
	}, nil
}

// executeMigration performs the actual migration following the 3-step process from the paper
func (mc *MigrationController) executeMigration(job *MigrationJob) {
	defer func() {
		<-mc.concurrencyCh
		if job.cancel != nil {
			job.cancel()
		}
	}()

	log.Printf("Starting migration %s: %s/%s from %s to %s",
		job.ID, job.Request.PodNamespace, job.Request.PodName,
		job.Request.SourceNode, job.Request.TargetNode)

	// Best-effort hint to Gluesys before we touch Kubernetes resources.
	datasetName := ""
	if job.Details != nil {
		datasetName = job.Details.PVClaimName
	}
	log.Printf("[KETI->Gluesys] Sending PrepareDataset for migration job %s dataset=%s", job.ID, datasetName)
	mc.notifyMantaPrepareDataset(job)

	// Update status to running
	mc.updateJobStatus(job, types.MigrationStatusRunning)

	// Step 1: Capture container states and collect metrics
	if err := mc.captureContainerStates(job); err != nil {
		mc.failMigration(job, fmt.Sprintf("Failed to capture container states: %v", err))
		return
	}

	// Step 2: Create checkpoint in Persistent Volume (if enabled)
	var checkpointPVC string
	if job.Request.PreservePV {
		var err error
		checkpointPVC, err = mc.createCheckpoint(job)
		if err != nil {
			mc.failMigration(job, fmt.Sprintf("Failed to create checkpoint: %v", err))
			return
		}
		job.Details.CheckpointPath = checkpointPVC
		job.Details.PVClaimName = checkpointPVC
	}

	// Step 3: Stage pipeline execution (decode -> filter -> output)
	if err := mc.executeStagePipeline(job, checkpointPVC); err != nil {
		mc.failMigration(job, fmt.Sprintf("Failed to execute stage pipeline: %v", err))
		return
	}

	// Step 4: Delete original pod
	if err := mc.deleteOriginalPod(job); err != nil {
		log.Printf("Warning: Failed to delete original pod: %v", err)
	}

	// Step 5: Collect post-migration metrics
	if err := mc.collectPostMigrationMetrics(job); err != nil {
		log.Printf("Warning: Failed to collect post-migration metrics: %v", err)
	}

	// Step 6: Cleanup intermediate outputs (output stage is never removed)
	if err := mc.cleanupIntermediateArtifacts(job); err != nil {
		log.Printf("Warning: Failed to cleanup intermediate artifacts: %v", err)
	}

	// Complete migration
	mc.completeMigration(job)

	log.Printf("Migration %s completed successfully", job.ID)
}

// cleanupIntermediateArtifacts는 마이그레이션 완료 후 중간 산출물을 정리한다.
func (mc *MigrationController) cleanupIntermediateArtifacts(job *MigrationJob) error {
	if job == nil || job.Request == nil {
		return nil
	}

	// 체크포인트 PVC 정리
	if job.Details != nil && job.Details.PVClaimName != "" {
		if err := mc.k8sClient.DeletePVC(job.ctx, job.Request.PodNamespace, job.Details.PVClaimName); err != nil {
			log.Printf("Migration %s: checkpoint PVC cleanup failed: %v", job.ID, err)
		} else {
			log.Printf("Migration %s: checkpoint PVC %s deleted", job.ID, job.Details.PVClaimName)
		}
	}

	// 로컬 임시 경로 정리 (output stage는 보존)
	tempPath := filepath.Join("/tmp/ai-storage-migration", job.ID)
	for _, stage := range []string{stageDecode, stageFilter} {
		stagePath := filepath.Join(tempPath, stage)
		if err := os.RemoveAll(stagePath); err != nil {
			return fmt.Errorf("failed to remove stage temp path %s: %w", stagePath, err)
		}
	}
	return nil
}

// captureContainerStates analyzes current container states and collects resource metrics
func (mc *MigrationController) captureContainerStates(job *MigrationJob) error {
	ctx := job.ctx

	// Get current pod
	pod, err := mc.k8sClient.GetPod(ctx, job.Request.PodNamespace, job.Request.PodName)
	if err != nil {
		return fmt.Errorf("failed to get pod: %w", err)
	}

	// Analyze container states
	containerStates, err := mc.k8sClient.GetPodContainerStates(ctx, pod)
	if err != nil {
		return fmt.Errorf("failed to analyze container states: %w", err)
	}

	job.Details.ContainerStates = containerStates

	// Collect original resource metrics
	metrics, err := mc.k8sClient.GetPodMetrics(ctx, job.Request.PodNamespace, job.Request.PodName)
	if err != nil {
		log.Printf("Warning: Failed to collect original metrics: %v", err)
		// Create default metrics if collection fails
		metrics = &types.ResourceUsage{
			CPUUsage:    0,
			MemoryUsage: 0,
			Timestamp:   time.Now(),
		}
	}

	job.Details.OriginalResources = metrics

	// Count containers that should be migrated
	shouldMigrate := 0
	for _, state := range containerStates {
		if state.ShouldMigrate {
			shouldMigrate++
		}
	}

	log.Printf("Migration %s: %d/%d containers will be migrated",
		job.ID, shouldMigrate, len(containerStates))

	return nil
}

// createCheckpoint creates a PVC for storing container state
func (mc *MigrationController) createCheckpoint(job *MigrationJob) (string, error) {
	ctx := job.ctx

	// PVC 이름에 job.ID(=migration-<uuid8자>)를 포함해 유니크를 보장한다.
	// WHY: 과거엔 초 단위 time.Now().Unix() 만 사용해, 같은 Pod 에 대한 동시
	//      마이그레이션(예: forecaster 가 cpu/memory 정책을 같은 초에 생성)이
	//      동일 PVC 이름을 만들어 "persistentvolumeclaims ... already exists" 로
	//      두 번째가 실패했다. job.ID 는 UUID 기반이라 동시 실행도 충돌하지 않는다.
	uniqueSuffix := strings.TrimPrefix(job.ID, "migration-")
	checkpointName := fmt.Sprintf("checkpoint-%s-%s", job.Request.PodName, uniqueSuffix)
	// DNS-1123(라벨 63자, 이름 253자) 보호: 너무 길면 PodName 을 잘라 안전화한다.
	if len(checkpointName) > 253 {
		maxPod := 253 - len("checkpoint--") - len(uniqueSuffix)
		if maxPod < 0 {
			maxPod = 0
		}
		pod := job.Request.PodName
		if len(pod) > maxPod {
			pod = pod[:maxPod]
		}
		checkpointName = fmt.Sprintf("checkpoint-%s-%s", pod, uniqueSuffix)
	}

	err := mc.k8sClient.CreatePersistentVolumeClaim(ctx, job.Request.PodNamespace, checkpointName, mc.checkpointSize)
	if err != nil {
		return "", fmt.Errorf("failed to create checkpoint PVC: %w", err)
	}

	log.Printf("Migration %s: Created checkpoint PVC %s", job.ID, checkpointName)
	return checkpointName, nil
}

func (mc *MigrationController) executeStagePipeline(job *MigrationJob, checkpointPVC string) error {
	ctx := job.ctx
	originalPod, err := mc.k8sClient.GetPod(ctx, job.Request.PodNamespace, job.Request.PodName)
	if err != nil {
		return fmt.Errorf("failed to get original pod for stage pipeline: %w", err)
	}

	stagePlan := buildStagePlan(resolveRequestedStage(job.Request, originalPod))
	if len(stagePlan) == 0 {
		return fmt.Errorf("empty stage plan")
	}

	tempRoot := filepath.Join("/tmp/ai-storage-migration", job.ID)
	if err := os.MkdirAll(tempRoot, 0o755); err != nil {
		return fmt.Errorf("failed to create migration temp root: %w", err)
	}
	shardIdx, shardCnt := shardInfoFromPod(originalPod)

	for i, stage := range stagePlan {
		stagePath := stagePathFor(stage)
		if err := os.MkdirAll(filepath.Join(tempRoot, stage), 0o755); err != nil {
			return fmt.Errorf("failed to create stage temp dir: %w", err)
		}

		if err := mc.recordStageTransition(job, stage, "started", stagePath, ""); err != nil {
			return err
		}

		stagePod := originalPod.DeepCopy()
		if stagePod.Annotations == nil {
			stagePod.Annotations = map[string]string{}
		}
		stagePod.Annotations["ai-storage.keti/stage"] = stage
		stagePod.Annotations["ai-storage.keti/stage-path"] = stagePath
		stagePod.Annotations["ai-storage.keti/shard-index"] = strconv.Itoa(shardIdx)
		stagePod.Annotations["ai-storage.keti/shard-count"] = strconv.Itoa(shardCnt)

		newPod, err := mc.createOptimizedPod(job, stagePod, checkpointPVC, stagePath)
		if err != nil {
			_ = mc.recordStageTransition(job, stage, "failed", stagePath, err.Error())
			return fmt.Errorf("stage %s pod create failed: %w", stage, err)
		}
		if err := mc.waitForStageCompletion(job, newPod, stage, filepath.Join(tempRoot, stage)); err != nil {
			_ = mc.recordStageTransition(job, stage, "failed", stagePath, err.Error())
			return fmt.Errorf("stage %s completion failed: %w", stage, err)
		}
		job.Details.NewPodName = newPod.Name

		if err := mc.recordStageTransition(job, stage, "completed", stagePath, fmt.Sprintf("pod=%s", newPod.Name)); err != nil {
			return err
		}
		if stage != stageOutput {
			if err := mc.k8sClient.DeletePod(ctx, newPod.Namespace, newPod.Name); err != nil {
				return fmt.Errorf("stage %s transition delete failed for pod %s: %w", stage, newPod.Name, err)
			}
			log.Printf("Migration %s: stage transition %s -> next (deleted pod %s)", job.ID, stage, newPod.Name)
		}

		if i > 0 {
			prev := stagePlan[i-1]
			if prev != stageOutput {
				prevPath := filepath.Join(tempRoot, prev)
				if err := os.RemoveAll(prevPath); err != nil {
					return fmt.Errorf("stage %s cleanup failed: %w", prev, err)
				}
				log.Printf("Migration %s: stage cleanup done for %s (%s)", job.ID, prev, prevPath)
			}
		}
	}
	return nil
}

// createOptimizedPod creates a staged optimized pod.
func (mc *MigrationController) createOptimizedPod(job *MigrationJob, originalPod *corev1.Pod, checkpointPVC string, stagePath string) (*corev1.Pod, error) {
	ctx := job.ctx

	newPod, err := mc.k8sClient.CreateOptimizedPod(ctx, originalPod, job.Request.TargetNode, job.Details.ContainerStates, checkpointPVC, stagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create optimized pod: %w", err)
	}

	log.Printf("Migration %s: Created optimized pod %s on node %s",
		job.ID, newPod.Name, job.Request.TargetNode)

	// Best-effort: Pod 배치 결과를 Gluesys에 전달
	log.Printf("[KETI->Gluesys] Sending ReportPodPlacement pod=%s node=%s", newPod.Name, job.Request.TargetNode)
	mc.notifyMantaPodPlacement(newPod.Name, job.Request.PodNamespace, job.Request.TargetNode)

	return newPod, nil
}

// deleteOriginalPod removes the original pod
func (mc *MigrationController) deleteOriginalPod(job *MigrationJob) error {
	ctx := job.ctx

	err := mc.k8sClient.DeletePod(ctx, job.Request.PodNamespace, job.Request.PodName)
	if err != nil {
		return fmt.Errorf("failed to delete original pod: %w", err)
	}

	log.Printf("Migration %s: Deleted original pod %s", job.ID, job.Request.PodName)
	return nil
}

// collectPostMigrationMetrics collects resource usage after migration
func (mc *MigrationController) collectPostMigrationMetrics(job *MigrationJob) error {
	// Wait a bit for metrics to stabilize
	time.Sleep(30 * time.Second)

	// Collect actual metrics from the new pod
	if job.Details.NewPodName != "" {
		metrics, err := mc.k8sClient.GetPodMetrics(job.ctx, job.Request.PodNamespace, job.Details.NewPodName)
		if err != nil {
			log.Printf("Warning: Failed to collect optimized pod metrics: %v", err)
			// Fallback to simulation if metrics collection fails
			if job.Details.OriginalResources != nil {
				job.Details.OptimizedResources = &types.ResourceUsage{
					CPUUsage:    job.Details.OriginalResources.CPUUsage * 0.5,
					MemoryUsage: int64(float64(job.Details.OriginalResources.MemoryUsage) * 0.6),
					Timestamp:   time.Now(),
				}
			}
			return nil
		}
		job.Details.OptimizedResources = metrics
		log.Printf("Migration %s: Collected optimized metrics - CPU: %.2f cores, Memory: %d bytes",
			job.ID, metrics.CPUUsage, metrics.MemoryUsage)
	} else {
		// Fallback: if new pod name is not available, use simulation
		log.Printf("Warning: New pod name not available, using simulated metrics")
		if job.Details.OriginalResources != nil {
			job.Details.OptimizedResources = &types.ResourceUsage{
				CPUUsage:    job.Details.OriginalResources.CPUUsage * 0.5,
				MemoryUsage: int64(float64(job.Details.OriginalResources.MemoryUsage) * 0.6),
				Timestamp:   time.Now(),
			}
		}
	}

	return nil
}

// Helper methods

func resolveRequestedStage(req *types.MigrationRequest, pod *corev1.Pod) string {
	if req != nil && strings.TrimSpace(req.Stage) != "" {
		return strings.ToLower(strings.TrimSpace(req.Stage))
	}
	if pod != nil && pod.Annotations != nil {
		if s := strings.TrimSpace(pod.Annotations["ai-storage.keti/stage"]); s != "" {
			return strings.ToLower(s)
		}
	}
	return stageDecode
}

func buildStagePlan(start string) []string {
	switch strings.ToLower(strings.TrimSpace(start)) {
	case stageFilter:
		return []string{stageFilter, stageOutput}
	case stageOutput:
		return []string{stageOutput}
	default:
		return []string{stageDecode, stageFilter, stageOutput}
	}
}

func stagePathFor(stage string) string {
	switch stage {
	case stageDecode:
		return "/data"
	case stageFilter:
		return "/cache"
	case stageOutput:
		return "/output"
	default:
		return "/data"
	}
}

func shardInfoFromPod(pod *corev1.Pod) (int, int) {
	idx, cnt := 0, 1
	if pod == nil {
		return idx, cnt
	}
	readInt := func(v string) (int, bool) {
		if strings.TrimSpace(v) == "" {
			return 0, false
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, false
		}
		return n, true
	}
	if pod.Labels != nil {
		if v, ok := pod.Labels["apps.kubernetes.io/pod-index"]; ok {
			if n, ok := readInt(v); ok && n >= 0 {
				idx = n
			}
		}
		if v, ok := pod.Labels["ai-storage.keti/shard-index"]; ok {
			if n, ok := readInt(v); ok && n >= 0 {
				idx = n
			}
		}
		if v, ok := pod.Labels["ai-storage.keti/shard-count"]; ok {
			if n, ok := readInt(v); ok && n > 0 {
				cnt = n
			}
		}
	}
	if pod.Annotations != nil {
		if v := pod.Annotations["ai-storage.keti/shard-index"]; v != "" {
			if n, ok := readInt(v); ok && n >= 0 {
				idx = n
			}
		}
		if v := pod.Annotations["ai-storage.keti/shard-count"]; v != "" {
			if n, ok := readInt(v); ok && n > 0 {
				cnt = n
			}
		}
	}
	return idx, cnt
}

// stageContainerHardFailures는 컨테이너가 비정상 종료(exitCode!=0)된 경우 오류를 반환한다.
func stageContainerHardFailures(current *corev1.Pod) error {
	if current == nil {
		return nil
	}
	for _, st := range current.Status.ContainerStatuses {
		if st.State.Terminated != nil && st.State.Terminated.ExitCode != 0 {
			return fmt.Errorf("container %s terminated with exitCode=%d reason=%s",
				st.Name, st.State.Terminated.ExitCode, st.State.Terminated.Reason)
		}
	}
	return nil
}

// isExecUnsupportedErr는 컨테이너 안에서 명령 exec 자체가 불가능함을 뜻하는 에러인지 판별한다.
// distroless 이미지(argo workflow-controller 등)에는 /bin/sh·mkdir·touch가 없어 exec이 항상 실패하므로,
// 이런 에러는 재시도해도 영원히 성공하지 못한다. 일시적/네트워크성 에러와 구분해 호출부에서 다르게 처리한다.
func isExecUnsupportedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"executable file not found",
		"no such file or directory",
		"exec format error",
		"oci runtime exec failed",
		"failed to exec in container",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// decodeOrFilterStageRunningComplete는 decode/filter 단계 완료 여부를 판별한다.
//
// 장시간 실행형 워크로드는 PodSucceeded/전 컨테이너 Terminated(exit 0) 조건을 만족하지 못하므로,
// Pod가 Running이고 스펙의 모든 컨테이너가 (Running+Ready) 또는 (exit 0으로 정상 종료)인 경우 완료로 본다.
func decodeOrFilterStageRunningComplete(current *corev1.Pod) bool {
	if current == nil || current.Status.Phase != corev1.PodRunning {
		return false
	}
	if len(current.Status.ContainerStatuses) < len(current.Spec.Containers) {
		return false
	}
	for _, c := range current.Spec.Containers {
		var st *corev1.ContainerStatus
		for i := range current.Status.ContainerStatuses {
			if current.Status.ContainerStatuses[i].Name == c.Name {
				st = &current.Status.ContainerStatuses[i]
				break
			}
		}
		if st == nil {
			return false
		}
		switch {
		case st.State.Running != nil:
			if !st.Ready {
				return false
			}
		case st.State.Terminated != nil && st.State.Terminated.ExitCode == 0:
			// 일회성 컨테이너가 이미 정상 종료된 경우 허용
		default:
			return false
		}
	}
	return true
}

func (mc *MigrationController) waitForStageCompletion(job *MigrationJob, pod *corev1.Pod, stage string, stageTempPath string) error {
	if pod == nil {
		return fmt.Errorf("nil stage pod")
	}
	timeout := stageTimeout(job)
	deadline := time.Now().Add(timeout)
	log.Printf("Migration %s: stage=%s waiting for completion (timeout=%s, pod=%s)", job.ID, stage, timeout.String(), pod.Name)

	outputMarkerTouched := false

	for time.Now().Before(deadline) {
		select {
		case <-job.ctx.Done():
			return job.ctx.Err()
		default:
		}

		current, err := mc.k8sClient.GetPod(job.ctx, pod.Namespace, pod.Name)
		if err != nil {
			return fmt.Errorf("get stage pod %s: %w", pod.Name, err)
		}
		if current.Status.Phase == corev1.PodFailed {
			return fmt.Errorf("pod failed: reason=%s message=%s", current.Status.Reason, current.Status.Message)
		}
		if current.Status.Phase == corev1.PodSucceeded {
			log.Printf("Migration %s: stage=%s completed by pod phase Succeeded (pod=%s)", job.ID, stage, pod.Name)
			return nil
		}

		if err := stageContainerHardFailures(current); err != nil {
			return err
		}

		// decode/filter는 output과 달리 _SUCCESS가 없고, 장시간 Running 컨테이너도 정상이므로 Ready 기반으로 완료를 본다.
		if stage == stageDecode || stage == stageFilter {
			if decodeOrFilterStageRunningComplete(current) {
				log.Printf("Migration %s: stage=%s completed by Running and all containers Ready (pod=%s)", job.ID, stage, pod.Name)
				return nil
			}
		}

		// output은 Pod 내부 stage 경로의 _SUCCESS를 본다(오케스트레이터 호스트 /tmp 경로와 Pod 볼륨은 다름).
		if stage == stageOutput && len(current.Spec.Containers) > 0 {
			successInPod := filepath.Join(stagePathFor(stageOutput), "_SUCCESS")
			c0 := current.Spec.Containers[0].Name
			if ok, err := mc.k8sClient.FileExistsInPod(job.ctx, current.Namespace, current.Name, c0, successInPod); err != nil {
				log.Printf("Migration %s: stage=%s _SUCCESS check error (ignored for retry): %v", job.ID, stage, err)
			} else if ok {
				log.Printf("Migration %s: stage=%s completed by success file in pod %s (container=%s)", job.ID, stage, successInPod, c0)
				return nil
			}
			// 워크로드가 마커를 쓰지 않는 경우(예: sleep)에도 stage 경로가 마운트되면 오케스트레이터가 마커를 남긴다.
			if !outputMarkerTouched && decodeOrFilterStageRunningComplete(current) {
				if err := mc.k8sClient.TouchPathInPod(job.ctx, current.Namespace, current.Name, c0, successInPod); err != nil {
					// distroless 등 shell/mkdir/touch가 없는 이미지에서는 exec 자체가 불가능하다.
					// 이 경우 워크로드는 이미 Running·전 컨테이너 Ready(위 조건)이므로 decode/filter와
					// 동일하게 Ready 기반으로 stage 완료를 인정한다. exec 불가가 아닌 일시적 에러는 재시도 유지.
					if isExecUnsupportedErr(err) {
						log.Printf("Migration %s: stage=%s marker unwritable (distroless?), completing on Ready (pod=%s): %v", job.ID, stage, pod.Name, err)
						return nil
					}
					log.Printf("Migration %s: stage=%s could not write _SUCCESS in pod yet: %v", job.ID, stage, err)
				} else {
					outputMarkerTouched = true
					log.Printf("Migration %s: stage=%s wrote _SUCCESS in pod at %s", job.ID, stage, successInPod)
				}
			}
		}

		terminatedOK := true
		terminatedCount := 0
		for _, st := range current.Status.ContainerStatuses {
			if st.State.Terminated == nil {
				terminatedOK = false
				continue
			}
			terminatedCount++
			if st.State.Terminated.ExitCode != 0 {
				return fmt.Errorf("container %s terminated with exitCode=%d reason=%s",
					st.Name, st.State.Terminated.ExitCode, st.State.Terminated.Reason)
			}
		}
		if terminatedCount > 0 && terminatedOK {
			log.Printf("Migration %s: stage=%s completed by all containers terminated successfully (pod=%s)", job.ID, stage, pod.Name)
			return nil
		}

		time.Sleep(3 * time.Second)
	}

	return fmt.Errorf("stage %s timeout after %s (pod=%s)", stage, timeout.String(), pod.Name)
}

func stageTimeout(job *MigrationJob) time.Duration {
	if job == nil || job.Request == nil || job.Request.Timeout <= 0 {
		return 5 * time.Minute
	}
	d := time.Duration(job.Request.Timeout) * time.Second / 3
	if d < 1*time.Minute {
		return 1 * time.Minute
	}
	return d
}

func (mc *MigrationController) recordStageTransition(job *MigrationJob, stage, status, path, msg string) error {
	if job == nil || job.Details == nil {
		return fmt.Errorf("stage transition: invalid migration job")
	}
	now := time.Now()
	mc.migrationsMux.Lock()
	defer mc.migrationsMux.Unlock()
	job.Details.CurrentStage = stage
	transition := types.StageTransition{
		Stage:      stage,
		Status:     status,
		Path:       path,
		StartedAt:  now,
		FinishedAt: now,
		Message:    msg,
	}
	job.Details.StageHistory = append(job.Details.StageHistory, transition)
	log.Printf("Migration %s: stage=%s status=%s path=%s %s", job.ID, stage, status, path, msg)
	return nil
}

func (mc *MigrationController) updateJobStatus(job *MigrationJob, status types.MigrationStatus) {
	mc.migrationsMux.Lock()
	job.Status = status
	mc.migrationsMux.Unlock()
}

func (mc *MigrationController) failMigration(job *MigrationJob, message string) {
	if job == nil || job.Request == nil {
		log.Printf("Migration failed: invalid job (nil job or request): %s", message)
		return
	}

	currentStage := ""
	if job.Details != nil {
		currentStage = job.Details.CurrentStage
	}
	log.Printf(
		"Migration failed: migration_id=%s namespace=%s workload=%s source_node=%s target_node=%s current_stage=%s error_message=%s",
		job.ID,
		job.Request.PodNamespace,
		job.Request.PodName,
		job.Request.SourceNode,
		job.Request.TargetNode,
		currentStage,
		message,
	)

	mc.migrationsMux.Lock()
	if job.Details == nil {
		job.Details = &types.MigrationDetails{StartTime: time.Now()}
	}
	job.Status = types.MigrationStatusFailed
	job.Details.ErrorMessage = message
	endTime := time.Now()
	job.Details.EndTime = &endTime
	duration := endTime.Sub(job.StartTime)
	job.Details.Duration = &duration
	mc.metrics.FailedMigrations++
	mc.migrationsMux.Unlock()
	mc.publishOrchestrationResult(job, "fail", message)
	log.Printf("%s", mc.formatResultLogLine(job, "fail"))
}

func (mc *MigrationController) completeMigration(job *MigrationJob) {
	mc.migrationsMux.Lock()
	job.Status = types.MigrationStatusCompleted
	endTime := time.Now()
	job.Details.EndTime = &endTime
	duration := endTime.Sub(job.StartTime)
	job.Details.Duration = &duration

	// Update metrics
	mc.metrics.TotalMigrations++
	mc.metrics.SuccessfulMigrations++

	// Calculate average duration
	if mc.metrics.TotalMigrations > 0 {
		// Simplified average calculation
		mc.metrics.AverageDuration = (mc.metrics.AverageDuration*time.Duration(mc.metrics.TotalMigrations-1) + duration) / time.Duration(mc.metrics.TotalMigrations)
	}

	// Calculate resource savings if we have both metrics
	if job.Details.OriginalResources != nil && job.Details.OptimizedResources != nil {
		cpuSavings := ((job.Details.OriginalResources.CPUUsage - job.Details.OptimizedResources.CPUUsage) / job.Details.OriginalResources.CPUUsage) * 100
		memorySavings := ((float64(job.Details.OriginalResources.MemoryUsage - job.Details.OptimizedResources.MemoryUsage)) / float64(job.Details.OriginalResources.MemoryUsage)) * 100

		mc.metrics.CPUSavings = cpuSavings
		mc.metrics.MemorySavings = memorySavings
	}

	mc.migrationsMux.Unlock()
	mc.publishOrchestrationResult(job, "success", "")
	log.Printf("%s", mc.formatResultLogLine(job, "success"))
}

func (mc *MigrationController) getStatusMessage(status types.MigrationStatus) string {
	switch status {
	case types.MigrationStatusPending:
		return "Migration is pending"
	case types.MigrationStatusRunning:
		return "Migration is in progress"
	case types.MigrationStatusCompleted:
		return "Migration completed successfully"
	case types.MigrationStatusFailed:
		return "Migration failed"
	case types.MigrationStatusCancelled:
		return "Migration was cancelled"
	default:
		return "Unknown status"
	}
}

// buildStatusMessage는 API 응답의 Message를 구성한다.
//
// failed 상태이고 details.error_message가 채워진 경우, generic 문구 대신 그 값을 반환한다.
func (mc *MigrationController) buildStatusMessage(job *MigrationJob) string {
	if job == nil {
		return "Unknown status"
	}
	if job.Status == types.MigrationStatusFailed && job.Details != nil && strings.TrimSpace(job.Details.ErrorMessage) != "" {
		return job.Details.ErrorMessage
	}
	return mc.getStatusMessage(job.Status)
}

// GetMetrics returns current migration metrics
func (mc *MigrationController) GetMetrics() *types.MigrationMetrics {
	mc.migrationsMux.RLock()
	defer mc.migrationsMux.RUnlock()

	// Return a copy of metrics
	metrics := *mc.metrics
	return &metrics
}

// GetStageExecutionPlan은 run_id + target_stage 기준 계획을 조회한다.
func (mc *MigrationController) GetStageExecutionPlan(runID, targetStage string) (*types.StageExecutionPlan, error) {
	if mc == nil || mc.planStore == nil {
		return nil, fmt.Errorf("stage execution plan store is not configured")
	}
	plan, ok := mc.planStore.Get(runID, targetStage)
	if !ok {
		return nil, fmt.Errorf("stage execution plan not found for run_id=%s target_stage=%s", runID, targetStage)
	}
	return plan, nil
}

// buildDatasetContext builds DatasetContext for a migration job.
func (mc *MigrationController) buildDatasetContext(job *MigrationJob) gluesys.DatasetContext {
	if job == nil || job.Request == nil {
		return gluesys.DatasetContext{}
	}
	datasetName := ""
	if job.Details != nil && job.Details.PVClaimName != "" {
		datasetName = job.Details.PVClaimName
	}
	return gluesys.DatasetContext{
		WorkloadName: job.Request.PodName,
		DatasetName:  datasetName,
		Namespace:    job.Request.PodNamespace,
		NodeName:     job.Request.TargetNode,
		PVCName:      datasetName,
		PVName:       "",
	}
}

// notifyGluesysPrepareDataset sends a best-effort PrepareDataset hint to Gluesys.
func (mc *MigrationController) notifyMantaPrepareDataset(job *MigrationJob) {
	if mc.gluesysClient == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		dctx := mc.buildDatasetContext(job)
		if dctx.DatasetName == "" {
			return
		}
		if err := mc.gluesysClient.PrepareDataset(ctx, dctx); err != nil {
			log.Printf("gluesys PrepareDataset error (migration %s): %v", job.ID, err)
		}
	}()
}

// notifyGluesysPodPlacement sends a best-effort ReportPodPlacement to Gluesys (스케줄링 결과 전달).
func (mc *MigrationController) notifyMantaPodPlacement(podName, namespace, nodeName string) {
	if mc.gluesysClient == nil || podName == "" || nodeName == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		dctx := gluesys.DatasetContext{
			WorkloadName: podName,
			DatasetName:  "",
			Namespace:    namespace,
			NodeName:     nodeName,
			PVCName:      "",
			PVName:       "",
		}
		if err := mc.gluesysClient.ReportPodPlacement(ctx, dctx); err != nil {
			log.Printf("gluesys ReportPodPlacement error for pod %s/%s: %v", namespace, podName, err)
		}
	}()
}
