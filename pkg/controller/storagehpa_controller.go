// Package controller implements the StorageHPA CRD controller
// CRD 기반 오토스케일러: Reconciler 패턴 사용
//
// REST API 방식 vs CRD 방식 차이:
//
//	REST API: HTTP 요청 → 메모리에 저장 → goroutine으로 모니터링
//	CRD:      kubectl apply → etcd에 저장 → Reconciler가 주기적으로 조정
//
// Reconciler 패턴:
//  1. 쿠버네티스가 리소스 변경 감지
//  2. Reconcile() 호출
//  3. 현재 상태(status) vs 원하는 상태(spec) 비교
//  4. 차이가 있으면 조정
//  5. 다시 큐에 넣고 반복
package controller

import (
	"context"
	"fmt"
	"log"
	"time"

	apollov1 "ai-storage-orchestrator/api/v1"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// 기본 Reconcile 주기 (15초마다 메트릭 체크)
	defaultRequeueInterval = 15 * time.Second
)

// StorageHPAReconciler reconciles a StorageHPA object
// CRD 컨트롤러의 핵심 구조체
type StorageHPAReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	K8sClient K8sClientInterface // 기존 K8s 클라이언트 재사용

	// 안정화 윈도우를 위한 스케일링 히스토리
	// key: namespace/name
	scaleHistory map[string]*scaleHistoryEntry
}

// scaleHistoryEntry는 스케일링 히스토리 추적용
type scaleHistoryEntry struct {
	scaleUpHistory   []scaleRecommendationEntry
	scaleDownHistory []scaleRecommendationEntry
}

type scaleRecommendationEntry struct {
	replicas  int32
	timestamp time.Time
}

// NewStorageHPAReconciler creates a new reconciler
func NewStorageHPAReconciler(client client.Client, scheme *runtime.Scheme, k8sClient K8sClientInterface) *StorageHPAReconciler {
	return &StorageHPAReconciler{
		Client:       client,
		Scheme:       scheme,
		K8sClient:    k8sClient,
		scaleHistory: make(map[string]*scaleHistoryEntry),
	}
}

// +kubebuilder:rbac:groups=apollo.keti.re.kr,resources=storagehpas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apollo.keti.re.kr,resources=storagehpas/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apollo.keti.re.kr,resources=storagehpas/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=metrics.k8s.io,resources=pods,verbs=get;list

// Reconcile는 StorageHPA 리소스의 현재 상태를 원하는 상태로 조정
// 이 함수가 CRD 컨트롤러의 핵심!
//
// 동작 방식:
//  1. StorageHPA 리소스 조회
//  2. 대상 워크로드의 현재 레플리카 수 조회
//  3. 메트릭 수집 (CPU, Memory, GPU, Storage I/O)
//  4. 원하는 레플리카 수 계산
//  5. 스케일링 필요시 워크로드 업데이트
//  6. StorageHPA status 업데이트
//  7. 15초 후 다시 Reconcile (RequeueAfter)
func (r *StorageHPAReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.Printf("[StorageHPA] Reconcile 시작: %s/%s", req.Namespace, req.Name)

	// 1. StorageHPA 리소스 조회
	var storageHPA apollov1.StorageHPA
	if err := r.Get(ctx, req.NamespacedName, &storageHPA); err != nil {
		// 리소스가 삭제된 경우 무시
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 히스토리 키
	historyKey := fmt.Sprintf("%s/%s", req.Namespace, req.Name)
	if r.scaleHistory[historyKey] == nil {
		r.scaleHistory[historyKey] = &scaleHistoryEntry{
			scaleUpHistory:   make([]scaleRecommendationEntry, 0),
			scaleDownHistory: make([]scaleRecommendationEntry, 0),
		}
	}

	// 2. 대상 워크로드 조회 및 현재 레플리카 수 확인
	currentReplicas, err := r.getCurrentReplicas(ctx, &storageHPA)
	if err != nil {
		log.Printf("[StorageHPA] %s: 워크로드 조회 실패: %v", req.Name, err)
		return r.updateStatusFailed(ctx, &storageHPA, err.Error())
	}

	// 3. 메트릭 수집
	metrics, err := r.collectMetrics(ctx, &storageHPA)
	if err != nil {
		log.Printf("[StorageHPA] %s: 메트릭 수집 실패 (시뮬레이션 사용): %v", req.Name, err)
		// 메트릭 수집 실패시 시뮬레이션 값 사용
		metrics = r.getSimulatedMetrics()
	}

	// 4. 원하는 레플리카 수 계산
	desiredReplicas := r.calculateDesiredReplicas(&storageHPA, currentReplicas, metrics)

	// 5. 안정화 윈도우 적용
	stabilizedReplicas := r.applyStabilizationWindow(&storageHPA, historyKey, currentReplicas, desiredReplicas)

	// 6. 스케일링 실행
	scaled := false
	if stabilizedReplicas != currentReplicas {
		if err := r.scaleWorkload(ctx, &storageHPA, stabilizedReplicas); err != nil {
			log.Printf("[StorageHPA] %s: 스케일링 실패: %v", req.Name, err)
			return r.updateStatusFailed(ctx, &storageHPA, err.Error())
		}
		scaled = true

		if stabilizedReplicas > currentReplicas {
			log.Printf("[StorageHPA] %s: 스케일 UP %d → %d (목표: %d)",
				req.Name, currentReplicas, stabilizedReplicas, desiredReplicas)
		} else {
			log.Printf("[StorageHPA] %s: 스케일 DOWN %d → %d (목표: %d)",
				req.Name, currentReplicas, stabilizedReplicas, desiredReplicas)
		}
	}

	// 7. Status 업데이트
	if err := r.updateStatus(ctx, &storageHPA, currentReplicas, stabilizedReplicas, metrics, scaled); err != nil {
		log.Printf("[StorageHPA] %s: Status 업데이트 실패: %v", req.Name, err)
		return ctrl.Result{}, err
	}

	// 8. 15초 후 다시 Reconcile
	return ctrl.Result{RequeueAfter: defaultRequeueInterval}, nil
}

// metricsData는 수집된 메트릭 데이터
type metricsData struct {
	cpuPercent             int32
	memoryPercent          int32
	gpuPercent             int32
	storageReadThroughput  int64
	storageWriteThroughput int64
	storageIOPS            int64
}

// getCurrentReplicas returns the current replica count of the target workload
func (r *StorageHPAReconciler) getCurrentReplicas(ctx context.Context, hpa *apollov1.StorageHPA) (int32, error) {
	switch hpa.Spec.WorkloadRef.Kind {
	case "Deployment":
		var deployment appsv1.Deployment
		err := r.Get(ctx, types.NamespacedName{
			Namespace: hpa.Namespace,
			Name:      hpa.Spec.WorkloadRef.Name,
		}, &deployment)
		if err != nil {
			return 0, fmt.Errorf("Deployment 조회 실패: %w", err)
		}
		if deployment.Spec.Replicas != nil {
			return *deployment.Spec.Replicas, nil
		}
		return 1, nil

	case "StatefulSet":
		var statefulset appsv1.StatefulSet
		err := r.Get(ctx, types.NamespacedName{
			Namespace: hpa.Namespace,
			Name:      hpa.Spec.WorkloadRef.Name,
		}, &statefulset)
		if err != nil {
			return 0, fmt.Errorf("StatefulSet 조회 실패: %w", err)
		}
		if statefulset.Spec.Replicas != nil {
			return *statefulset.Spec.Replicas, nil
		}
		return 1, nil

	default:
		return 0, fmt.Errorf("지원하지 않는 워크로드 종류: %s", hpa.Spec.WorkloadRef.Kind)
	}
}

// collectMetrics collects metrics for the target workload
func (r *StorageHPAReconciler) collectMetrics(ctx context.Context, hpa *apollov1.StorageHPA) (*metricsData, error) {
	// K8sClient를 통해 메트릭 수집
	cpuPercent, memoryPercent, gpuPercent, readMBps, writeMBps, iops, err := r.K8sClient.GetWorkloadPodMetrics(
		ctx,
		hpa.Namespace,
		hpa.Spec.WorkloadRef.Name,
	)
	if err != nil {
		return nil, err
	}

	return &metricsData{
		cpuPercent:             cpuPercent,
		memoryPercent:          memoryPercent,
		gpuPercent:             gpuPercent,
		storageReadThroughput:  readMBps,
		storageWriteThroughput: writeMBps,
		storageIOPS:            iops,
	}, nil
}

// getSimulatedMetrics returns simulated metrics when real metrics are unavailable
func (r *StorageHPAReconciler) getSimulatedMetrics() *metricsData {
	// 시뮬레이션 값 (테스트용)
	return &metricsData{
		cpuPercent:             50 + int32(time.Now().Unix()%40),
		memoryPercent:          45 + int32(time.Now().Unix()%35),
		gpuPercent:             40 + int32(time.Now().Unix()%50),
		storageReadThroughput:  300 + int64(time.Now().Unix()%200),
		storageWriteThroughput: 80 + int64(time.Now().Unix()%70),
		storageIOPS:            2000 + int64(time.Now().Unix()%2000),
	}
}

// calculateDesiredReplicas calculates the desired replica count based on metrics
func (r *StorageHPAReconciler) calculateDesiredReplicas(hpa *apollov1.StorageHPA, currentReplicas int32, metrics *metricsData) int32 {
	if currentReplicas == 0 {
		currentReplicas = 1
	}

	recommendations := []int32{}

	// CPU 기반 계산
	if hpa.Spec.TargetCPUPercent != nil && metrics.cpuPercent > 0 {
		targetCPU := *hpa.Spec.TargetCPUPercent
		cpuDesired := int32(float64(currentReplicas) * float64(metrics.cpuPercent) / float64(targetCPU))
		recommendations = append(recommendations, cpuDesired)
	}

	// Memory 기반 계산
	if hpa.Spec.TargetMemoryPercent != nil && metrics.memoryPercent > 0 {
		targetMem := *hpa.Spec.TargetMemoryPercent
		memDesired := int32(float64(currentReplicas) * float64(metrics.memoryPercent) / float64(targetMem))
		recommendations = append(recommendations, memDesired)
	}

	// GPU 기반 계산
	if hpa.Spec.TargetGPUPercent != nil && metrics.gpuPercent > 0 {
		targetGPU := *hpa.Spec.TargetGPUPercent
		gpuDesired := int32(float64(currentReplicas) * float64(metrics.gpuPercent) / float64(targetGPU))
		recommendations = append(recommendations, gpuDesired)
	}

	// Storage Read 기반 계산 (AI/ML 데이터 로딩에 중요!)
	if hpa.Spec.TargetStorageReadThroughput != nil && metrics.storageReadThroughput > 0 {
		targetRead := *hpa.Spec.TargetStorageReadThroughput
		readDesired := int32(float64(currentReplicas) * float64(metrics.storageReadThroughput) / float64(targetRead))
		recommendations = append(recommendations, readDesired)
		log.Printf("[StorageHPA] Storage Read: %d MB/s (목표: %d MB/s) → %d replicas",
			metrics.storageReadThroughput, targetRead, readDesired)
	}

	// Storage Write 기반 계산
	if hpa.Spec.TargetStorageWriteThroughput != nil && metrics.storageWriteThroughput > 0 {
		targetWrite := *hpa.Spec.TargetStorageWriteThroughput
		writeDesired := int32(float64(currentReplicas) * float64(metrics.storageWriteThroughput) / float64(targetWrite))
		recommendations = append(recommendations, writeDesired)
	}

	// Storage IOPS 기반 계산
	if hpa.Spec.TargetStorageIOPS != nil && metrics.storageIOPS > 0 {
		targetIOPS := *hpa.Spec.TargetStorageIOPS
		iopsDesired := int32(float64(currentReplicas) * float64(metrics.storageIOPS) / float64(targetIOPS))
		recommendations = append(recommendations, iopsDesired)
	}

	// 최대값 선택 (가장 보수적인 스케일 다운, 가장 적극적인 스케일 업)
	desiredReplicas := currentReplicas
	if len(recommendations) > 0 {
		desiredReplicas = recommendations[0]
		for _, rec := range recommendations {
			if rec > desiredReplicas {
				desiredReplicas = rec
			}
		}
	}

	// Min/Max 제약 적용
	if desiredReplicas < hpa.Spec.MinReplicas {
		desiredReplicas = hpa.Spec.MinReplicas
	}
	if desiredReplicas > hpa.Spec.MaxReplicas {
		desiredReplicas = hpa.Spec.MaxReplicas
	}

	// MaxScaleChange 적용
	if desiredReplicas > currentReplicas {
		maxChange := hpa.Spec.GetMaxScaleChangeForScaleUp()
		if maxChange > 0 && desiredReplicas-currentReplicas > maxChange {
			desiredReplicas = currentReplicas + maxChange
		}
	} else if desiredReplicas < currentReplicas {
		maxChange := hpa.Spec.GetMaxScaleChangeForScaleDown()
		if maxChange > 0 && currentReplicas-desiredReplicas > maxChange {
			desiredReplicas = currentReplicas - maxChange
		}
	}

	return desiredReplicas
}

// applyStabilizationWindow applies stabilization window to prevent flapping
func (r *StorageHPAReconciler) applyStabilizationWindow(hpa *apollov1.StorageHPA, historyKey string, currentReplicas, desiredReplicas int32) int32 {
	now := time.Now()
	history := r.scaleHistory[historyKey]

	recommendation := scaleRecommendationEntry{
		replicas:  desiredReplicas,
		timestamp: now,
	}

	if desiredReplicas > currentReplicas {
		// 스케일 업
		history.scaleUpHistory = append(history.scaleUpHistory, recommendation)

		stabilizationWindow := hpa.Spec.GetStabilizationWindowForScaleUp()
		if stabilizationWindow > 0 {
			cutoffTime := now.Add(-time.Duration(stabilizationWindow) * time.Second)
			validRecs := []scaleRecommendationEntry{}
			for _, rec := range history.scaleUpHistory {
				if rec.timestamp.After(cutoffTime) {
					validRecs = append(validRecs, rec)
				}
			}
			history.scaleUpHistory = validRecs

			if len(history.scaleUpHistory) > 0 {
				maxReplicas := history.scaleUpHistory[0].replicas
				for _, rec := range history.scaleUpHistory {
					if rec.replicas > maxReplicas {
						maxReplicas = rec.replicas
					}
				}
				return maxReplicas
			}
		}

	} else if desiredReplicas < currentReplicas {
		// 스케일 다운 (더 보수적으로)
		history.scaleDownHistory = append(history.scaleDownHistory, recommendation)

		stabilizationWindow := hpa.Spec.GetStabilizationWindowForScaleDown()
		cutoffTime := now.Add(-time.Duration(stabilizationWindow) * time.Second)
		validRecs := []scaleRecommendationEntry{}
		for _, rec := range history.scaleDownHistory {
			if rec.timestamp.After(cutoffTime) {
				validRecs = append(validRecs, rec)
			}
		}
		history.scaleDownHistory = validRecs

		if len(history.scaleDownHistory) > 0 {
			maxReplicas := history.scaleDownHistory[0].replicas
			for _, rec := range history.scaleDownHistory {
				if rec.replicas > maxReplicas {
					maxReplicas = rec.replicas
				}
			}
			return maxReplicas
		}

	} else {
		// 변경 없음 - 히스토리 클리어
		history.scaleUpHistory = []scaleRecommendationEntry{}
		history.scaleDownHistory = []scaleRecommendationEntry{}
	}

	return desiredReplicas
}

// scaleWorkload scales the target workload
func (r *StorageHPAReconciler) scaleWorkload(ctx context.Context, hpa *apollov1.StorageHPA, replicas int32) error {
	switch hpa.Spec.WorkloadRef.Kind {
	case "Deployment":
		var deployment appsv1.Deployment
		err := r.Get(ctx, types.NamespacedName{
			Namespace: hpa.Namespace,
			Name:      hpa.Spec.WorkloadRef.Name,
		}, &deployment)
		if err != nil {
			return err
		}

		deployment.Spec.Replicas = &replicas
		return r.Update(ctx, &deployment)

	case "StatefulSet":
		var statefulset appsv1.StatefulSet
		err := r.Get(ctx, types.NamespacedName{
			Namespace: hpa.Namespace,
			Name:      hpa.Spec.WorkloadRef.Name,
		}, &statefulset)
		if err != nil {
			return err
		}

		statefulset.Spec.Replicas = &replicas
		return r.Update(ctx, &statefulset)

	default:
		return fmt.Errorf("지원하지 않는 워크로드: %s", hpa.Spec.WorkloadRef.Kind)
	}
}

// updateStatus updates the StorageHPA status
func (r *StorageHPAReconciler) updateStatus(ctx context.Context, hpa *apollov1.StorageHPA, currentReplicas, desiredReplicas int32, metrics *metricsData, scaled bool) error {
	now := metav1.Now()

	// Status 업데이트
	hpa.Status.CurrentReplicas = currentReplicas
	hpa.Status.DesiredReplicas = desiredReplicas
	hpa.Status.CurrentCPUPercent = metrics.cpuPercent
	hpa.Status.CurrentMemoryPercent = metrics.memoryPercent
	hpa.Status.CurrentGPUPercent = metrics.gpuPercent
	hpa.Status.CurrentStorageReadThroughput = metrics.storageReadThroughput
	hpa.Status.CurrentStorageWriteThroughput = metrics.storageWriteThroughput
	hpa.Status.CurrentStorageIOPS = metrics.storageIOPS
	hpa.Status.Phase = apollov1.StorageHPAPhaseActive
	hpa.Status.Message = "오토스케일러 활성"
	hpa.Status.LastUpdated = &now

	if scaled {
		hpa.Status.LastScaleTime = &now
		if desiredReplicas > currentReplicas {
			hpa.Status.ScaleUpCount++
		} else {
			hpa.Status.ScaleDownCount++
		}
	}

	// Status 서브리소스 업데이트
	return r.Status().Update(ctx, hpa)
}

// updateStatusFailed updates status to failed
func (r *StorageHPAReconciler) updateStatusFailed(ctx context.Context, hpa *apollov1.StorageHPA, message string) (ctrl.Result, error) {
	now := metav1.Now()
	hpa.Status.Phase = apollov1.StorageHPAPhaseFailed
	hpa.Status.Message = message
	hpa.Status.LastUpdated = &now

	if err := r.Status().Update(ctx, hpa); err != nil {
		return ctrl.Result{}, err
	}

	// 실패해도 30초 후 재시도
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *StorageHPAReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&apollov1.StorageHPA{}).
		Complete(r)
}
