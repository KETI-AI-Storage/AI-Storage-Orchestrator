package controller

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"ai-storage-orchestrator/pkg/config"
	"ai-storage-orchestrator/pkg/gluesys"
	"ai-storage-orchestrator/pkg/types"

	"github.com/google/uuid"
)

// ProvisioningController manages storage provisioning for AI/ML workloads
type ProvisioningController struct {
	k8sClient        K8sClientInterface
	gluesysClient    gluesys.Integration
	planStore        *StageExecutionPlanStore
	provisionings    map[string]*ProvisioningJob
	provisioningsMux sync.RWMutex
	metrics          *types.ProvisioningMetrics

	// Storage profiles for different performance requirements
	storageProfiles map[string]types.StorageProfile

	// storageClassMap 는 정규화된 tier(L1/L2/L3/S3) → 실제 K8s StorageClass 매핑이다.
	// WHY: 기존 하드코딩 switch(tierToStorageClass) 를 ConfigMap(config.yaml provisioning
	//      섹션) 기반으로 대체하기 위한 필드. SC 명명 변경 시 재빌드 없이 ConfigMap 만 수정.
	storageClassMap map[string]string
	// defaultStorageClass 는 알 수 없는 tier 보정용 SC 다(설정 가능).
	defaultStorageClass string
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

// NewProvisioningController creates a new provisioning controller.
// gluesysClient can be nil; in that case Gluesys integration is disabled.
//
// provCfg 는 tier→StorageClass 매핑 설정이다. nil 이거나 일부 키만 채워져 있어도
// 빌트인 기본값(storage-l1..s3) 위에 병합되므로 항상 안전한 매핑이 만들어진다.
func NewProvisioningController(k8sClient K8sClientInterface, gluesysClient gluesys.Integration, planStore *StageExecutionPlanStore, provCfg *config.ProvisioningConfig) *ProvisioningController {
	// nil 또는 부분 매핑이면 빌트인 기본값으로 보정한다(self-protecting).
	// WHY: ConfigMap 이 optional 이라 호출자가 nil 을 넘길 수 있고, YAML 이 일부 tier 만
	//      정의할 수도 있다. 기본값 위에 덮어써 어떤 입력이든 4개 tier 가 항상 채워지게 한다.
	cfg := config.DefaultProvisioningConfig()
	if provCfg != nil {
		if strings.TrimSpace(provCfg.DefaultStorageClass) != "" {
			cfg.DefaultStorageClass = provCfg.DefaultStorageClass
		}
		for tier, sc := range provCfg.TierStorageClasses {
			if strings.TrimSpace(tier) == "" || strings.TrimSpace(sc) == "" {
				continue
			}
			cfg.TierStorageClasses[strings.ToUpper(strings.TrimSpace(tier))] = sc
		}
	}

	pc := &ProvisioningController{
		k8sClient:     k8sClient,
		gluesysClient: gluesysClient,
		planStore:     planStore,
		provisionings: make(map[string]*ProvisioningJob),
		metrics: &types.ProvisioningMetrics{
			TotalProvisionings:      0,
			ActiveProvisionings:     0,
			TotalStorageProvisioned: "0Gi",
			AverageProvisionTime:    0,
		},
		storageProfiles:     make(map[string]types.StorageProfile),
		storageClassMap:     cfg.TierStorageClasses,
		defaultStorageClass: cfg.DefaultStorageClass,
	}

	// Initialize storage profiles
	pc.initializeStorageProfiles()

	return pc
}

// normalizeTier는 외부에서 들어온 tier 이름을 Gluesys 표준 L1/L2/L3/S3 로 정규화한다.
//
// 인식 가능한 입력(대소문자 무관):
//   - "L1", "burst", "cache"     → "L1"
//   - "L2", "performance"        → "L2"
//   - "L3", "capacity"           → "L3"
//   - "S3", "archive"            → "S3"
//
// Parameters:
//   - tier: 외부에서 들어온 tier 문자열.
//
// Returns:
//   - string: 표준 tier 이름. 인식 실패 시 빈 문자열.
func normalizeTier(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "l1", "burst", "cache":
		return "L1"
	case "l2", "performance":
		return "L2"
	case "l3", "capacity":
		return "L3"
	case "s3", "archive":
		return "S3"
	default:
		return ""
	}
}

// tierToStorageClass는 정책에서 사용하는 tier 이름을 실제 클러스터 K8s StorageClass 이름으로
// 매핑한다. 매핑은 ConfigMap(config.yaml provisioning.tier_storage_classes)에서 주입되며,
// 부재 시 빌트인 기본값(storage-l1/l2/l3/s3)을 사용한다.
//
// WHY: orchestrator validateRequest 가 tier 화이트리스트로 검증하더라도, PVC 의
//
//	storageClassName 은 클러스터에 실제로 존재하는 SC 이름이어야 Bound 가 된다.
//	정책 레이어(tier)와 K8s 레이어(SC name)를 분리하고, 그 매핑을 코드가 아닌 설정으로 빼서
//	SC 명명이 바뀌어도 재빌드 없이 ConfigMap 만 고치면 되게 한다. 기존 burst/performance/
//	capacity/archive 입력은 호출 전에 normalizeTier 로 L1~S3 로 정규화돼 들어온다.
//	알 수 없는 tier 면 빈 문자열을 반환하고, 호출 측에서 defaultStorageClass 로 보정한다.
func (pc *ProvisioningController) tierToStorageClass(tier string) string {
	if sc, ok := pc.storageClassMap[normalizeTier(tier)]; ok {
		return sc
	}
	return ""
}

// tierForStorageClass 는 SC 이름으로부터 tier 를 역추론한다.
// WHY: 알 수 없는 tier 를 default SC 로 보정할 때, 응답/라벨의 ActualClass 가 그 SC 와
//
//	일관된 tier 를 갖도록 한다. 매핑에 없으면 보수적으로 "S3"(아카이브)로 둔다.
func (pc *ProvisioningController) tierForStorageClass(sc string) string {
	for tier, name := range pc.storageClassMap {
		if name == sc {
			return tier
		}
	}
	return "S3"
}

// initializeStorageProfiles sets up predefined storage performance profiles.
// WHY: tier 화이트리스트는 Gluesys 표준 L1/L2/L3/S3 4종으로만 운용한다.
//
//	(기존 burst/performance/capacity/archive 는 호환 입력으로만 받고, 내부 정책은
//	모두 L1~S3 키로 통일한다.)
func (pc *ProvisioningController) initializeStorageProfiles() {
	pc.storageProfiles["L1"] = types.StorageProfile{
		Name:                "L1",
		ReadThroughputMBps:  800,
		WriteThroughputMBps: 400,
		IOPS:                5000,
		Description:         "L1 cache/burst tier → storage-l1 (반복 접근, cache, ultra-low latency)",
	}

	pc.storageProfiles["L2"] = types.StorageProfile{
		Name:                "L2",
		ReadThroughputMBps:  400,
		WriteThroughputMBps: 200,
		IOPS:                10000,
		Description:         "L2 NVMe/hot tier → storage-l2 (전처리/학습/추론 입력, hot-data)",
	}

	pc.storageProfiles["L3"] = types.StorageProfile{
		Name:                "L3",
		ReadThroughputMBps:  500,
		WriteThroughputMBps: 250,
		IOPS:                3000,
		Description:         "L3 HDD/capacity tier → storage-l3 (원본 대용량, 중간 산출물, warm/cold)",
	}

	pc.storageProfiles["S3"] = types.StorageProfile{
		Name:                "S3",
		ReadThroughputMBps:  200,
		WriteThroughputMBps: 100,
		IOPS:                1000,
		Description:         "S3 archive/object tier → storage-s3 (백업, 오래된 로그, 장기보관)",
	}
}

// CreateProvisioning creates a new storage provisioning for AI/ML workload
func (pc *ProvisioningController) CreateProvisioning(req *types.ProvisioningRequest) (*types.ProvisioningResponse, error) {
	// Validate request
	if err := pc.validateRequest(req); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	pc.saveStageExecutionPlanForProvisioning(req)

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

func (pc *ProvisioningController) saveStageExecutionPlanForProvisioning(req *types.ProvisioningRequest) {
	if pc == nil || pc.planStore == nil || req == nil {
		return
	}
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		runID = strings.TrimSpace(req.Annotations["run_id"])
	}
	if runID == "" {
		runID = strings.TrimSpace(req.WorkloadName)
	}
	targetStage := strings.TrimSpace(req.TargetStage)
	if targetStage == "" {
		targetStage = strings.TrimSpace(req.Annotations["target_stage"])
	}
	if targetStage == "" {
		targetStage = "storage"
	}
	policyAnnotations := map[string]string{
		"ai-storage/policy-name": req.Annotations["policy_name"],
		"ai-storage/policy-action": req.Annotations["policy_action"],
	}
	storageAnnotations := map[string]string{
		"ai-storage/storage-class": req.StorageClass,
		"ai-storage/workload-type": req.WorkloadType,
	}
	for key, value := range req.Annotations {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		storageAnnotations[key] = value
	}
	pc.planStore.Save(&types.StageExecutionPlan{
		RunID:              runID,
		TargetStage:        targetStage,
		Kind:               "provisioning",
		ResourceRequests:   map[string]string{},
		SchedulerName:      "ai-storage-scheduler",
		NodeSelector:       cloneStringMap(req.Labels),
		QueueLabel:         targetStage,
		StorageAnnotations: storageAnnotations,
		PolicyAnnotations:  policyAnnotations,
	})
}

// executeProvisioning creates the actual PVC via Kubernetes API and waits for it to be Bound.
func (pc *ProvisioningController) executeProvisioning(job *ProvisioningJob) {
	startTime := time.Now()
	pc.updateJobStatus(job, types.ProvisioningStatusCreating)

	// tier 이름을 실제 K8s StorageClass 이름으로 변환한다.
	// WHY: PVC.spec.storageClassName 은 클러스터에 실제 존재하는 SC 이름이어야 Bound 가
	//      된다. 정책/API 레이어에서는 tier 이름(Gluesys L1/L2/L3/S3)으로 통일해 사용하고,
	//      K8s 생성 시점에만 변환한다. 기존 burst/performance/capacity/archive 입력은
	//      normalize 후 동일한 변환 경로를 탄다.
	tier := normalizeTier(job.Details.ActualClass)
	scName := pc.tierToStorageClass(tier)
	if scName == "" {
		// 안전망: 알 수 없는 tier 면 config 의 default_storage_class 로 보정한다.
		// WHY: 클러스터에 없는 SC 면 PVC 가 영구 Pending 되므로 항상 존재하는 기본 SC 로
		//      떨어뜨린다. 기존엔 tier="S3" 고정이었으나 이제 설정값(defaultStorageClass)을 따른다.
		log.Printf("Provisioning %s: WARN unknown tier %q, falling back to default SC %q",
			job.ID, job.Details.ActualClass, pc.defaultStorageClass)
		scName = pc.defaultStorageClass
		// ActualClass 라벨이 보정된 SC 와 일관되도록 tier 도 역추론한다.
		tier = pc.tierForStorageClass(scName)
	}
	// ActualClass 도 표준 L1~S3 로 정규화해 두어 외부 응답이 일관되도록 한다.
	job.Details.ActualClass = tier

	log.Printf("Provisioning %s: Creating PVC %s (size=%s, tier=%s, sc=%s, access=%s) in namespace %s",
		job.ID, job.Details.PVCName, job.Details.ActualSize,
		tier, scName, job.Request.AccessMode, job.Request.WorkloadNamespace)

	pvcLabels := map[string]string{
		"app":             "ai-storage-orchestrator",
		"component":       "provisioning",
		"provisioning-id": job.ID,
		"workload-name":   job.Request.WorkloadName,
		"workload-type":   job.Request.WorkloadType,
		// WHY: 운영 도구에서 tier 로 필터링할 수 있도록 라벨에도 노출한다.
		"ai-storage/selected-tier": tier,
	}

	// WHY: 사용자 요구사항 — tier 결정 결과를 annotation 으로도 남겨 Pod/PVC 검사 시
	//      별도 도구에서 동일 값을 읽어갈 수 있게 한다.
	pvcAnnotations := map[string]string{
		"ai-storage/selected-tier":     tier,
		"storage.keti.io/tier-hint":    tier,
		"ai-storage/storage-class":     scName,
		"ai-storage/provisioning-id":   job.ID,
	}

	err := pc.k8sClient.CreatePVCWithAnnotations(
		job.ctx,
		job.Request.WorkloadNamespace,
		job.Details.PVCName,
		job.Details.ActualSize,
		scName,
		job.Request.AccessMode,
		pvcLabels,
		pvcAnnotations,
	)
	if err != nil {
		log.Printf("Provisioning %s: PVC creation failed: %v", job.ID, err)
		pc.updateJobStatus(job, types.ProvisioningStatusFailed)
		pc.provisioningsMux.Lock()
		pc.metrics.ActiveProvisionings--
		pc.provisioningsMux.Unlock()
		return
	}

	// WaitForFirstConsumer 모드 SC 의 PVC 는 consumer Pod 없이는 Pending 으로 머무른다.
	// binder Pod 을 짧게 띄워 binding 을 유도한 뒤 정리한다.
	binderPod := fmt.Sprintf("pvc-binder-%s", strings.ToLower(job.ID))
	if len(binderPod) > 50 {
		binderPod = binderPod[:50]
	}
	if bindErr := pc.k8sClient.CreateBindingPodForPVC(job.ctx, job.Request.WorkloadNamespace, job.Details.PVCName, binderPod); bindErr != nil {
		log.Printf("Provisioning %s: WARN binder pod create failed: %v (PVC may stay Pending if SC=WaitForFirstConsumer)", job.ID, bindErr)
	} else {
		log.Printf("Provisioning %s: binder pod %s/%s started for PVC %s", job.ID, job.Request.WorkloadNamespace, binderPod, job.Details.PVCName)
		defer func() {
			_ = pc.k8sClient.DeletePod(context.Background(), job.Request.WorkloadNamespace, binderPod)
		}()
	}

	// Wait up to 5 minutes for the PVC to be Bound by the storage provisioner
	if err := pc.k8sClient.WaitForPVCReady(job.ctx, job.Request.WorkloadNamespace, job.Details.PVCName, 300); err != nil {
		log.Printf("Provisioning %s: PVC did not become Bound: %v", job.ID, err)
		pc.updateJobStatus(job, types.ProvisioningStatusFailed)
		pc.provisioningsMux.Lock()
		pc.metrics.ActiveProvisionings--
		pc.provisioningsMux.Unlock()
		return
	}

	pc.updateJobStatus(job, types.ProvisioningStatusReady)
	readyTime := time.Now()
	job.Details.ReadyAt = &readyTime
	job.Details.UpdatedAt = &readyTime

	provisionTime := time.Since(startTime).Seconds()
	pc.provisioningsMux.Lock()
	if pc.metrics.AverageProvisionTime == 0 {
		pc.metrics.AverageProvisionTime = provisionTime
	} else {
		pc.metrics.AverageProvisionTime = (pc.metrics.AverageProvisionTime + provisionTime) / 2
	}
	pc.metrics.ActiveProvisionings--
	pc.provisioningsMux.Unlock()

	log.Printf("Provisioning %s: PVC %s is Bound and ready (%.2fs)", job.ID, job.Details.PVCName, provisionTime)

	// Best-effort hint to Gluesys that this dataset/PVC will be used.
	log.Printf("[KETI->Gluesys] Sending PrepareDataset for provisioning job %s dataset=%s", job.ID, job.Details.PVCName)
	pc.notifyMantaPrepareDataset(job)
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

// selectStorageClass selects appropriate storage class based on requirements.
// WHY: 반환값은 정책 레이어의 tier 이름(Gluesys L1/L2/L3/S3) 이고,
//
//	실제 K8s SC 이름으로의 변환은 tierToStorageClass 가 담당한다.
func (pc *ProvisioningController) selectStorageClass(req *types.ProvisioningRequest) string {
	// If performance requirements are specified, select based on them
	if req.RequiredReadThroughput > 0 || req.RequiredWriteThroughput > 0 || req.RequiredIOPS > 0 {
		// High throughput / IOPS → L1 (cache/burst) 또는 L2 (NVMe/hot)
		if req.RequiredReadThroughput >= 600 || req.RequiredWriteThroughput >= 300 {
			return "L1"
		}

		// High IOPS required → L2 (NVMe/hot)
		if req.RequiredIOPS >= 8000 {
			return "L2"
		}

		// Balanced requirements → L3 (HDD/capacity)
		return "L3"
	}

	// Select based on workload type
	switch req.WorkloadType {
	case "training":
		// Training workloads → L2 (NVMe/hot, low latency)
		return "L2"
	case "inference":
		// Inference → L3 (HDD/capacity, balanced)
		return "L3"
	case "data-pipeline":
		// Data pipelines benefit from cache/burst → L1
		return "L1"
	default:
		// Long-term/general purpose → S3 (archive)
		return "S3"
	}
}

// EnsureStandardPVCsForWorkload 는 Gluesys 스토리지 표준에 따라
// 특정 워크로드(namespace/name, type)에 필요한 PVC들을 생성/보장한다.
// 이미 동일 PVC가 provisioning map 에 존재하면 재생성하지 않는다.
func (pc *ProvisioningController) EnsureStandardPVCsForWorkload(workloadNamespace, workloadName string, workloadType types.WorkloadType, needOutput, needCache bool) error {
	policy := types.GetStoragePolicy(workloadType, needOutput, needCache)
	if len(policy) == 0 {
		log.Printf("[StoragePolicy] workload=%s/%s type=%s -> no PVC required", workloadNamespace, workloadName, workloadType)
		return nil
	}

	log.Printf("[StoragePolicy] workload=%s/%s type=%s -> %d PVC required", workloadNamespace, workloadName, workloadType, len(policy))

	for _, req := range policy {
		pvcName := types.BuildPVCName(workloadName, req.Role)

		// 이미 생성된 PVC/provisioning 이 있는지 확인
		if pc.hasProvisioningForPVC(workloadNamespace, pvcName) {
			log.Printf("[Provisioning] PVC %s/%s already ensured (role=%s, class=%s)", workloadNamespace, pvcName, req.Role, req.StorageClass)
			continue
		}

		// ProvisioningRequest 생성 (auto_size=true, StorageClass는 정책에서 지정)
		provReq := &types.ProvisioningRequest{
			WorkloadName:      workloadName,
			WorkloadNamespace: workloadNamespace,
			WorkloadType:      string(workloadType),
			AutoSize:          true,
			StorageClass:      req.StorageClass,
			MountPath:         req.MountPath,
		}

		resp, err := pc.CreateProvisioning(provReq)
		if err != nil {
			log.Printf("[Provisioning] failed to ensure PVC %s/%s (role=%s, class=%s): %v", workloadNamespace, pvcName, req.Role, req.StorageClass, err)
			return err
		}

		log.Printf("[Provisioning] ensured PVC %s/%s via provisioning_id=%s (role=%s, class=%s, mount=%s)",
			workloadNamespace, resp.Details.PVCName, resp.ProvisioningID, req.Role, req.StorageClass, req.MountPath)
	}

	return nil
}

// hasProvisioningForPVC 는 동일 네임스페이스/워크로드에 대해 이미 PVC가 존재하는지를
// ProvisioningController 내부 상태를 기준으로 확인한다.
func (pc *ProvisioningController) hasProvisioningForPVC(namespace, pvcName string) bool {
	pc.provisioningsMux.RLock()
	defer pc.provisioningsMux.RUnlock()

	for _, job := range pc.provisionings {
		if job == nil || job.Details == nil || job.Request == nil {
			continue
		}
		if job.Details.PVCName == pvcName && job.Request.WorkloadNamespace == namespace {
			return true
		}
	}
	return false
}

// buildPVCName 는 워크로드 이름과 역할( input / output / cache )에 따라 사람이 이해하기 쉬운 PVC 이름을 만든다.
// 예: training-job -> training-job-input, training-job-output, training-job-cache
// PVC 이름 생성 로직은 types.BuildPVCName 으로 이동했다.

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

	// Best-effort hint to Gluesys before we delete the PVC.
	log.Printf("[KETI->Gluesys] Sending ReleaseDatasetHint dataset=%s", job.Details.PVCName)
	pc.notifyMantaReleaseDataset(job)

	// Cancel the job context (stops any in-flight WaitForPVCReady)
	job.cancel()
	pvcName := job.Details.PVCName
	namespace := job.Request.WorkloadNamespace

	job.Status = types.ProvisioningStatusDeleting
	pc.provisioningsMux.Unlock()

	log.Printf("Provisioning %s: Deleting PVC %s/%s", provisioningID, namespace, pvcName)

	// Use background context — job ctx is already cancelled above
	delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := pc.k8sClient.DeletePVC(delCtx, namespace, pvcName); err != nil {
		log.Printf("Provisioning %s: PVC deletion failed (continuing cleanup): %v", provisioningID, err)
	}

	pc.provisioningsMux.Lock()
	delete(pc.provisionings, provisioningID)
	pc.metrics.ActiveProvisionings--
	pc.provisioningsMux.Unlock()

	log.Printf("Provisioning %s: Deleted successfully", provisioningID)
	return nil
}

// buildDatasetContext builds DatasetContext for a provisioning job.
func (pc *ProvisioningController) buildDatasetContext(job *ProvisioningJob) gluesys.DatasetContext {
	if job == nil || job.Request == nil || job.Details == nil {
		return gluesys.DatasetContext{}
	}
	return gluesys.DatasetContext{
		WorkloadName: job.Request.WorkloadName,
		DatasetName:  job.Details.PVCName,
		Namespace:    job.Request.WorkloadNamespace,
		NodeName:     "",
		PVCName:      job.Details.PVCName,
		PVName:       "",
	}
}

// notifyGluesysPrepareDataset sends a best-effort PrepareDataset hint to Gluesys.
func (pc *ProvisioningController) notifyMantaPrepareDataset(job *ProvisioningJob) {
	if pc.gluesysClient == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		dctx := pc.buildDatasetContext(job)
		if dctx.DatasetName == "" {
			return
		}
		if err := pc.gluesysClient.PrepareDataset(ctx, dctx); err != nil {
			log.Printf("gluesys PrepareDataset error (provisioning %s): %v", job.ID, err)
		}
	}()
}

// notifyGluesysReleaseDataset sends a best-effort ReleaseDatasetHint to Gluesys.
func (pc *ProvisioningController) notifyMantaReleaseDataset(job *ProvisioningJob) {
	if pc.gluesysClient == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		dctx := pc.buildDatasetContext(job)
		if dctx.DatasetName == "" {
			return
		}
		if err := pc.gluesysClient.ReleaseDatasetHint(ctx, dctx); err != nil {
			log.Printf("gluesys ReleaseDatasetHint error (provisioning %s): %v", job.ID, err)
		}
	}()
}


// GetRecommendation provides storage recommendations for a workload
func (pc *ProvisioningController) GetRecommendation(workloadType string) *types.WorkloadStorageRecommendation {
	var recommendation types.WorkloadStorageRecommendation
	recommendation.WorkloadType = workloadType

	switch workloadType {
	case "training":
		recommendation.RecommendedSize = "500Gi"
		recommendation.RecommendedClass = "L2"
		recommendation.RecommendedProfile = pc.storageProfiles["L2"]
		recommendation.Reasoning = "AI training workloads need NVMe/hot (L2) for low-latency dataset streaming during training iterations"

	case "inference":
		recommendation.RecommendedSize = "100Gi"
		recommendation.RecommendedClass = "L3"
		recommendation.RecommendedProfile = pc.storageProfiles["L3"]
		recommendation.Reasoning = "Inference workloads need balanced HDD/capacity (L3) for loading models and serving requests"

	case "data-pipeline":
		recommendation.RecommendedSize = "250Gi"
		recommendation.RecommendedClass = "L1"
		recommendation.RecommendedProfile = pc.storageProfiles["L1"]
		recommendation.Reasoning = "Data pipelines benefit from cache/burst (L1) for high-throughput intermediate I/O"

	default:
		recommendation.RecommendedSize = "200Gi"
		recommendation.RecommendedClass = "S3"
		recommendation.RecommendedProfile = pc.storageProfiles["S3"]
		recommendation.Reasoning = "Long-term archive (S3) for unknown workload types"
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

	// Validate storage class if provided.
	// WHY: Gluesys 표준 tier 4종(L1/L2/L3/S3) 만 허용한다. 기존 burst/performance/
	//      capacity/archive 입력은 호환을 위해 normalize 후 화이트리스트와 매칭한다.
	//      잘못된 tier 가 들어오면 PVC 의 storageClassName 이 클러스터에 없는 값이 되어
	//      영구 Pending 된다.
	if req.StorageClass != "" {
		normalized := normalizeTier(req.StorageClass)
		if normalized == "" {
			return fmt.Errorf("invalid storage_class: %s (valid tiers: L1, L2, L3, S3; legacy burst/cache/performance/capacity/archive 도 호환 입력으로 허용)", req.StorageClass)
		}
		if _, exists := pc.storageProfiles[normalized]; !exists {
			return fmt.Errorf("invalid storage_class: %s (no profile for tier %s)", req.StorageClass, normalized)
		}
		req.StorageClass = normalized // 이후 단계에서 일관된 L1~S3 사용
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
