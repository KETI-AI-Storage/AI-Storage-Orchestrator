package k8s

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ai-storage-orchestrator/pkg/types"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Client wraps Kubernetes client with migration-specific functionality
type Client struct {
	clientset        kubernetes.Interface
	metricsClientset metricsclientset.Interface
	config           *rest.Config
}

// NewClient creates a new Kubernetes client
func NewClient(kubeconfig string) (*Client, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes config: %w", err)
	}

	// InClusterConfig 등이 QPS=5/Burst=10을 설정하는 경우가 있어,
	// 다수 Autoscaler 고루틴이 동시에 API를 호출하면 rate limiter Wait가
	// context deadline과 충돌한다(오케스트레이터 로그에 확인됨).
	config.QPS = 100
	config.Burst = 200

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	metricsClientset, err := metricsclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics clientset: %w", err)
	}

	return &Client{
		clientset:        clientset,
		metricsClientset: metricsClientset,
		config:           config,
	}, nil
}

// GetPod retrieves a pod by name and namespace.
//
// 호환 목적의 fallback을 포함한다. 기존 migration 코드가 Deployment 이름을
// target pod name처럼 넘기는 경우가 있었기 때문에, exact Pod 조회가 NotFound이면
// workload resolver로 실제 Running Pod를 찾는다.
func (c *Client) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	pod, err := c.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return pod, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	// WHY: OrchestrationPolicy.targetWorkload가 Deployment/Job/Workflow 이름일 수 있다.
	//      migration/preemption 계열은 실제 Pod가 필요하므로 selector 기반으로 해석한다.
	pods, resolvedKind, resolveErr := c.ResolveTargetPods(ctx, namespace, name, WorkloadTypeAuto, PolicyTypeMigration, "")
	if resolveErr != nil {
		return nil, resolveErr
	}
	if len(pods) == 0 {
		return nil, NewWaitingForTargetError(namespace, name, resolvedKind, PolicyTypeMigration, "no running target pod")
	}
	return pods[0].DeepCopy(), nil
}

// GetPodContainerStates analyzes container states in a pod
func (c *Client) GetPodContainerStates(ctx context.Context, pod *corev1.Pod) ([]types.ContainerState, error) {
	var states []types.ContainerState

	for _, container := range pod.Spec.Containers {
		var containerStatus corev1.ContainerStatus

		// Find matching container status
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == container.Name {
				containerStatus = status
				break
			}
		}

		state := types.ContainerState{
			Name:         container.Name,
			RestartCount: containerStatus.RestartCount,
		}

		// Determine container state based on Kubernetes container state
		if containerStatus.State.Waiting != nil {
			state.State = "waiting"
			state.ShouldMigrate = false // Don't migrate waiting containers
		} else if containerStatus.State.Running != nil {
			state.State = "running"
			state.ShouldMigrate = true // Migrate running containers
		} else if containerStatus.State.Terminated != nil {
			if containerStatus.State.Terminated.ExitCode == 0 {
				state.State = "completed"
				state.ShouldMigrate = false // Don't migrate completed containers
			} else {
				state.State = "failed"
				state.ShouldMigrate = true // Migrate failed containers for retry
			}
		}

		states = append(states, state)
	}

	return states, nil
}

// checkpointStorageClass 는 마이그레이션 checkpoint PVC 전용 StorageClass 다.
// WHY: 클러스터 default SC(storage-l2 등)는 volumeBindingMode=WaitForFirstConsumer 라,
//
//	checkpoint PVC 가 "Pod 이 써야 바인딩"되는데 migrated Pod 은 "PVC 가 바인딩돼야
//	시작"되어 상호 대기 데드락이 발생한다(마이그레이션 영구 정지).
//	Immediate 바인딩 SC 를 써서 Pod 과 무관하게 즉시 PVC 가 Bound 되도록 한다.
//	(nfs-subdir provisioner 는 Immediate 바인딩 지원.)
const checkpointStorageClass = "checkpoint-immediate"

// CreatePersistentVolumeClaim creates a PVC for checkpointing container state (migration use)
func (c *Client) CreatePersistentVolumeClaim(ctx context.Context, namespace, name string, size string) error {
	return c.CreatePVC(ctx, namespace, name, size, checkpointStorageClass, "ReadWriteOnce", map[string]string{
		"app":       "ai-storage-orchestrator",
		"component": "migration-checkpoint",
	})
}

// CreatePVC creates a PersistentVolumeClaim with the given parameters.
// storageClass may be empty (uses cluster default). accessMode defaults to ReadWriteOnce.
func (c *Client) CreatePVC(ctx context.Context, namespace, name, size, storageClass, accessMode string, labels map[string]string) error {
	return c.CreatePVCWithAnnotations(ctx, namespace, name, size, storageClass, accessMode, labels, nil)
}

// CreatePVCWithAnnotations 는 CreatePVC 와 동일하나 annotations 도 함께 박는다.
//
// WHY: provisioning 정책은 tier 결정 결과를 PVC annotation
//
//	(ai-storage/selected-tier, storage.keti.io/tier-hint) 으로 남겨야 한다.
//	기존 CreatePVC 시그니처를 그대로 유지하기 위해 별도 헬퍼로 분리.
func (c *Client) CreatePVCWithAnnotations(ctx context.Context, namespace, name, size, storageClass, accessMode string, labels, annotations map[string]string) error {
	if accessMode == "" {
		accessMode = "ReadWriteOnce"
	}
	am := corev1.PersistentVolumeAccessMode(accessMode)

	mergedLabels := map[string]string{
		"app":       "ai-storage-orchestrator",
		"component": "provisioning",
	}
	for k, v := range labels {
		mergedLabels[k] = v
	}

	mergedAnnotations := map[string]string{}
	for k, v := range annotations {
		if v != "" {
			mergedAnnotations[k] = v
		}
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      mergedLabels,
			Annotations: mergedAnnotations,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{am},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(size),
				},
			},
		},
	}

	if storageClass != "" {
		pvc.Spec.StorageClassName = &storageClass
	}

	_, err := c.clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create PVC %s/%s: %w", namespace, name, err)
	}
	return nil
}

// CreateBindingPodForPVC 는 WaitForFirstConsumer 모드 StorageClass 의 PVC 를 즉시
// Bound 시키기 위해 해당 PVC 를 마운트하는 임시 1-shot Pod 을 띄운다.
//
// WHY: ai-storage demo 환경의 storage-{l1,l2,l3,s3} (Gluesys 표준) SC 는 모두
//
//	volumeBindingMode=WaitForFirstConsumer 라서 PVC 를 만들기만 해서는 Pending
//	상태로 남는다. provisioning 정책이 "PVC Bound" 까지를 책임지도록 PVC 직후
//	binder Pod 을 띄워 binding 을 유도한다. binder Pod 은 종료 후 호출자가 삭제한다.
func (c *Client) CreateBindingPodForPVC(ctx context.Context, namespace, pvcName, podName string) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":        "ai-storage-orchestrator",
				"component":  "provisioning-binder",
				"target-pvc": pvcName,
			},
			// WHY: webhook 의 sidecar 주입을 회피해 binder Pod 이 빠르게 Terminate 되도록 한다.
			Annotations: map[string]string{
				"keti-ai-storage-injection":            "disabled",
				"insight-trace.keti.io/main-container": "binder",
				"mlops.keti.io/main-container":         "binder",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "binder",
					Image:   "busybox:1.36",
					Command: []string{"sh", "-c", "ls -la /mnt/target >/dev/null 2>&1; exit 0"},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "target", MountPath: "/mnt/target"},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "target",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}
	_, err := c.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create binder pod %s/%s for pvc %s: %w", namespace, podName, pvcName, err)
	}
	return nil
}

// DeletePVC deletes a PersistentVolumeClaim.
func (c *Client) DeletePVC(ctx context.Context, namespace, name string) error {
	err := c.clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete PVC %s/%s: %w", namespace, name, err)
	}
	return nil
}

// WaitForPVCReady polls until the PVC reaches Bound phase or the timeout expires.
func (c *Client) WaitForPVCReady(ctx context.Context, namespace, name string, timeoutSeconds int) error {
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	for time.Now().Before(deadline) {
		pvc, err := c.clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get PVC %s/%s: %w", namespace, name, err)
		}
		if pvc.Status.Phase == corev1.ClaimBound {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("PVC %s/%s did not become Bound within %ds", namespace, name, timeoutSeconds)
}

// DeletePod deletes a pod gracefully
func (c *Client) DeletePod(ctx context.Context, namespace, name string) error {
	gracePeriod := int64(30) // 30 seconds grace period

	return c.clientset.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
}

// migrationManagedVolumes are volume (and matching mount) names owned by the
// migration pipeline. They are stripped from the inherited spec before fresh
// ones are attached, so re-migrating a pod that is itself a previous migration
// product does not produce a duplicate volume (which makes the pod invalid).
var migrationManagedVolumes = map[string]bool{
	"checkpoint-volume":       true,
	"migration-stage-scratch": true,
}

func withoutManagedVolumes(vols []corev1.Volume) []corev1.Volume {
	out := vols[:0:0]
	for _, v := range vols {
		if !migrationManagedVolumes[v.Name] {
			out = append(out, v)
		}
	}
	return out
}

func withoutManagedMounts(mounts []corev1.VolumeMount) []corev1.VolumeMount {
	out := mounts[:0:0]
	for _, m := range mounts {
		if !migrationManagedVolumes[m.Name] {
			out = append(out, m)
		}
	}
	return out
}

// CreateOptimizedPod creates a new pod with only running containers.
func (c *Client) CreateOptimizedPod(ctx context.Context, originalPod *corev1.Pod, targetNode string, containerStates []types.ContainerState, checkpointPVC string, stageMountPath string) (*corev1.Pod, error) {
	newPod := buildOptimizedPodSpec(originalPod, targetNode, containerStates, checkpointPVC, stageMountPath)
	return c.clientset.CoreV1().Pods(newPod.Namespace).Create(ctx, newPod, metav1.CreateOptions{})
}

// buildOptimizedPodSpec constructs the optimized (migrated) pod spec from the
// original pod: it keeps only the containers that should migrate and attaches a
// fresh checkpoint volume. Pure (no API calls) so it can be unit-tested.
func buildOptimizedPodSpec(originalPod *corev1.Pod, targetNode string, containerStates []types.ContainerState, checkpointPVC string, stageMountPath string) *corev1.Pod {
	// Create new pod spec based on original but optimized
	newPod := originalPod.DeepCopy()

	// Clear status and metadata that should not be copied
	newPod.Status = corev1.PodStatus{}
	// GenerateName 으로 K8s 가 충돌 없는 유니크 이름을 부여하게 한다.
	// WHY: 과거엔 "%s-migrated-%d"(초 단위 timestamp)로 직접 이름을 정해, 같은 Pod 의
	//      동시 마이그레이션이 같은 이름을 만들어 `pods "..." already exists` 로 실패했다.
	//      Create() 반환값(newPod.Name)에 실제 부여된 이름이 담기므로 호출부는 그대로 동작한다.
	//      DNS-1123(이름 253자) 보호: GenerateName prefix(<=247자) + 임의 suffix.
	migratedPrefix := fmt.Sprintf("%s-migrated-", originalPod.Name)
	if len(migratedPrefix) > 247 {
		migratedPrefix = migratedPrefix[:247]
	}
	newPod.ObjectMeta = metav1.ObjectMeta{
		GenerateName: migratedPrefix,
		Namespace:    originalPod.Namespace,
		Labels:       map[string]string{},
		Annotations:  originalPod.Annotations,
	}

	// ReplicaSet/Deployment selector와 겹치지 않도록 원본 labels를 복제하지 않고
	// migration 전용 라벨만 부여한다.
	newPod.Labels["migration.ai-storage/original-pod"] = originalPod.Name
	newPod.Labels["migration.ai-storage/target-node"] = targetNode
	newPod.Labels["migration.ai-storage/job"] = "true"

	// Pin to the chosen target node. NodeName is authoritative and bypasses the
	// scheduler, so any node-placement constraints inherited from the source pod
	// can only make the *target* kubelet reject the migrated pod with a
	// NodeAffinity/selector mismatch (they pin to the source node) — they can never
	// help, since NodeName already fixes placement. Strip them. Capability remains
	// enforced by the pod's resource requests, and Tolerations are kept (untouched)
	// so target-node taints are still tolerated.
	newPod.Spec.NodeName = targetNode
	newPod.Spec.NodeSelector = nil
	if newPod.Spec.Affinity != nil {
		newPod.Spec.Affinity.NodeAffinity = nil
		if newPod.Spec.Affinity.PodAffinity == nil && newPod.Spec.Affinity.PodAntiAffinity == nil {
			newPod.Spec.Affinity = nil
		}
	}

	// Filter containers - only include those that should be migrated
	var optimizedContainers []corev1.Container
	needsStageScratch := false
	for _, container := range newPod.Spec.Containers {
		for _, state := range containerStates {
			if container.Name == state.Name && state.ShouldMigrate {
				// Drop any migration-managed mounts inherited from a prior
				// migration so the add-mount logic below adds exactly one.
				container.VolumeMounts = withoutManagedMounts(container.VolumeMounts)
				hasMountPath := func(path string) bool {
					for _, vm := range container.VolumeMounts {
						if vm.MountPath == path {
							return true
						}
					}
					return false
				}

				// Add checkpoint volume mount if specified
				if checkpointPVC != "" {
					if stageMountPath == "" {
						stageMountPath = "/migration-checkpoint"
					}
					mountPath := stageMountPath
					if hasMountPath(mountPath) {
						mountPath = "/migration-checkpoint"
					}
					if !hasMountPath(mountPath) {
						container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
							Name:      "checkpoint-volume",
							MountPath: mountPath,
						})
					}
				} else if stageMountPath != "" {
					// 체크포인트 PVC가 없을 때도 stage 경로가 필요하다. 기존 볼륨과 겹치면 emptyDir를 붙이지 않는다.
					if !hasMountPath(stageMountPath) {
						container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
							Name:      "migration-stage-scratch",
							MountPath: stageMountPath,
						})
						needsStageScratch = true
					}
				}
				optimizedContainers = append(optimizedContainers, container)
				break
			}
		}
	}

	newPod.Spec.Containers = optimizedContainers

	// Drop migration-managed volumes inherited from a prior migration (they
	// reference a stale checkpoint PVC) so the fresh ones below don't collide
	// by name -> spec.volumes Duplicate value.
	newPod.Spec.Volumes = withoutManagedVolumes(newPod.Spec.Volumes)

	// Add checkpoint volume if specified
	if checkpointPVC != "" {
		newPod.Spec.Volumes = append(newPod.Spec.Volumes, corev1.Volume{
			Name: "checkpoint-volume",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: checkpointPVC,
				},
			},
		})
	} else if needsStageScratch {
		newPod.Spec.Volumes = append(newPod.Spec.Volumes, corev1.Volume{
			Name: "migration-stage-scratch",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}

	return newPod
}

// GetPodMetrics retrieves CPU and memory metrics for a pod
func (c *Client) GetPodMetrics(ctx context.Context, namespace, name string) (*types.ResourceUsage, error) {
	podMetrics, err := c.metricsClientset.MetricsV1beta1().PodMetricses(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod metrics: %w", err)
	}

	var totalCPU, totalMemory int64

	for _, container := range podMetrics.Containers {
		cpu := container.Usage[corev1.ResourceCPU]
		memory := container.Usage[corev1.ResourceMemory]

		totalCPU += cpu.MilliValue()  // Convert to millicores
		totalMemory += memory.Value() // Bytes
	}

	return &types.ResourceUsage{
		CPUUsage:    float64(totalCPU) / 1000.0, // Convert millicores to cores
		MemoryUsage: totalMemory,
		Timestamp:   podMetrics.Timestamp.Time,
	}, nil
}

// WaitForPodReady waits for a pod to be in Ready state
func (c *Client) WaitForPodReady(ctx context.Context, namespace, name string, timeout time.Duration) error {
	watchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watch, err := c.clientset.CoreV1().Pods(namespace).Watch(watchCtx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", name).String(),
	})
	if err != nil {
		return fmt.Errorf("failed to watch pod: %w", err)
	}
	defer watch.Stop()

	for event := range watch.ResultChan() {
		if pod, ok := event.Object.(*corev1.Pod); ok {
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
					return nil
				}
			}
		}
	}

	return fmt.Errorf("timeout waiting for pod to be ready")
}

const (
	WorkloadTypeAuto        = "auto"
	WorkloadTypeDeployment  = "Deployment"
	WorkloadTypeStatefulSet = "StatefulSet"
	WorkloadTypeReplicaSet  = "ReplicaSet"
	WorkloadTypeJob         = "Job"
	WorkloadTypePod         = "Pod"
	WorkloadTypeWorkflow    = "Workflow"

	PolicyTypeAutoscaling   = "autoscaling"
	PolicyTypeMigration     = "migration"
	PolicyTypePreemption    = "preemption"
	PolicyTypeCaching       = "caching"
	PolicyTypeLoadBalancing = "loadbalancing"
	PolicyTypeProvisioning  = "provisioning"
)

// WaitingForTargetError는 정책 대상 리소스/Pod가 아직 생성되지 않았을 때 반환한다.
// 컨트롤러는 이 에러를 Failed가 아니라 status=WaitingForTarget + requeue로 처리해야 한다.
type WaitingForTargetError struct {
	Namespace    string
	Name         string
	WorkloadType string
	PolicyType   string
	Reason       string
}

func (e *WaitingForTargetError) Error() string {
	return fmt.Sprintf("waiting for target namespace=%s name=%s workloadType=%s policyType=%s reason=%s",
		e.Namespace, e.Name, e.WorkloadType, e.PolicyType, e.Reason)
}

func NewWaitingForTargetError(namespace, name, workloadType, policyType, reason string) *WaitingForTargetError {
	return &WaitingForTargetError{
		Namespace:    namespace,
		Name:         name,
		WorkloadType: normalizeWorkloadType(workloadType),
		PolicyType:   normalizePolicyType(policyType),
		Reason:       reason,
	}
}

// IsWaitingForTarget는 호출부에서 status=WaitingForTarget/requeue 판정에 사용한다.
func IsWaitingForTarget(err error) bool {
	var waiting *WaitingForTargetError
	return errors.As(err, &waiting)
}

// WorkloadTarget은 OrchestrationPolicy.targetWorkload를 실제 Kubernetes 대상과 Pod 목록으로 해석한 결과다.
type WorkloadTarget struct {
	Kind        string
	Namespace   string
	Name        string
	Selector    string
	Stage       string
	Pods        []corev1.Pod
	RunningPods []corev1.Pod
}

// ResolveTargetPods는 migration/preemption처럼 최종적으로 Pod가 필요한 정책에서 사용한다.
func (c *Client) ResolveTargetPods(ctx context.Context, namespace, name, workloadType, policyType, stage string) ([]corev1.Pod, string, error) {
	target, err := c.ResolveWorkloadTarget(ctx, namespace, name, workloadType, policyType, stage, true)
	if err != nil {
		return nil, "", err
	}
	return target.RunningPods, target.Kind, nil
}

// ResolveWorkloadTarget은 targetWorkload를 kind별로 해석한다.
//
// 자동 탐색 순서:
//
//	Deployment -> StatefulSet -> Job -> Pod -> Workflow
//
// 정책별 허용 kind:
//
//	autoscaling  : Deployment/StatefulSet/ReplicaSet
//	migration    : Deployment/StatefulSet/Job/Pod/Workflow
//	preemption   : Deployment/StatefulSet/Job/Pod/Workflow
//	caching 등   : Deployment/StatefulSet/Job/Pod/Workflow
//
// target이 아직 없으면 WaitingForTargetError를 반환한다.
func (c *Client) ResolveWorkloadTarget(ctx context.Context, namespace, name, workloadType, policyType, stage string, requireRunningPod bool) (*WorkloadTarget, error) {
	if namespace == "" || name == "" {
		return nil, fmt.Errorf("invalid target: namespace and name are required (namespace=%q name=%q)", namespace, name)
	}

	policyType = normalizePolicyType(policyType)
	requestedKind := normalizeWorkloadType(workloadType)
	candidateKinds := candidateWorkloadKinds(policyType, requestedKind)

	var lastErr error
	for _, kind := range candidateKinds {
		target, err := c.resolveWorkloadTargetByKind(ctx, namespace, name, kind, stage)
		if err != nil {
			if apierrors.IsNotFound(err) || IsWaitingForTarget(err) {
				lastErr = err
				continue
			}
			lastErr = err
			continue
		}

		if requireRunningPod && len(target.RunningPods) == 0 {
			return nil, NewWaitingForTargetError(namespace, name, target.Kind, policyType,
				fmt.Sprintf("target resolved but no running pod found (selector=%s pods=%d)", target.Selector, len(target.Pods)))
		}
		return target, nil
	}

	if lastErr != nil && !apierrors.IsNotFound(lastErr) && !IsWaitingForTarget(lastErr) {
		return nil, lastErr
	}
	return nil, NewWaitingForTargetError(namespace, name, requestedKind, policyType, "target resource not found by configured kind order")
}

func (c *Client) resolveWorkloadTargetByKind(ctx context.Context, namespace, name, kind, stage string) (*WorkloadTarget, error) {
	kind = normalizeWorkloadType(kind)

	switch kind {
	case WorkloadTypeDeployment:
		deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		selector := metav1.FormatLabelSelector(deployment.Spec.Selector)
		pods, err := c.listPodsBySelector(ctx, namespace, selector)
		if err != nil {
			return nil, err
		}
		return newWorkloadTarget(WorkloadTypeDeployment, namespace, name, selector, stage, pods), nil

	case WorkloadTypeStatefulSet:
		statefulSet, err := c.clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		selector := metav1.FormatLabelSelector(statefulSet.Spec.Selector)
		pods, err := c.listPodsBySelector(ctx, namespace, selector)
		if err != nil {
			return nil, err
		}
		return newWorkloadTarget(WorkloadTypeStatefulSet, namespace, name, selector, stage, pods), nil

	case WorkloadTypeReplicaSet:
		replicaSet, err := c.clientset.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		selector := metav1.FormatLabelSelector(replicaSet.Spec.Selector)
		pods, err := c.listPodsBySelector(ctx, namespace, selector)
		if err != nil {
			return nil, err
		}
		return newWorkloadTarget(WorkloadTypeReplicaSet, namespace, name, selector, stage, pods), nil

	case WorkloadTypeJob:
		job, err := c.clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}

		selectors := make([]string, 0, 3)
		if job.Spec.Selector != nil {
			selectors = append(selectors, metav1.FormatLabelSelector(job.Spec.Selector))
		}
		selectors = append(selectors,
			labels.SelectorFromSet(labels.Set{"batch.kubernetes.io/job-name": name}).String(),
			labels.SelectorFromSet(labels.Set{"job-name": name}).String(),
		)

		pods, selector, err := c.listPodsBySelectors(ctx, namespace, selectors)
		if err != nil {
			return nil, err
		}
		return newWorkloadTarget(WorkloadTypeJob, namespace, name, selector, stage, pods), nil

	case WorkloadTypePod:
		pod, err := c.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return newWorkloadTarget(WorkloadTypePod, namespace, name,
			fields.OneTermEqualSelector("metadata.name", name).String(), stage, []corev1.Pod{*pod}), nil

	case WorkloadTypeWorkflow:
		selectors := []string{
			labels.SelectorFromSet(labels.Set{"workflows.argoproj.io/workflow": name}).String(),
		}
		if stage != "" {
			selectors = []string{selectors[0] + "," + labels.SelectorFromSet(labels.Set{"pipeline.kubeflow.org/stage": stage}).String()}
		}

		pods, selector, err := c.listPodsBySelectors(ctx, namespace, selectors)
		if err != nil {
			return nil, err
		}
		if len(pods) == 0 {
			return nil, NewWaitingForTargetError(namespace, name, WorkloadTypeWorkflow, "", fmt.Sprintf("workflow pods not found (selector=%s)", selector))
		}
		return newWorkloadTarget(WorkloadTypeWorkflow, namespace, name, selector, stage, pods), nil

	default:
		return nil, fmt.Errorf("unsupported workload type: %s", kind)
	}
}

func newWorkloadTarget(kind, namespace, name, selector, stage string, pods []corev1.Pod) *WorkloadTarget {
	sortPods(pods)
	running := make([]corev1.Pod, 0, len(pods))
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning {
			running = append(running, pod)
		}
	}
	return &WorkloadTarget{
		Kind:        kind,
		Namespace:   namespace,
		Name:        name,
		Selector:    selector,
		Stage:       stage,
		Pods:        pods,
		RunningPods: running,
	}
}

func (c *Client) listPodsBySelector(ctx context.Context, namespace, selector string) ([]corev1.Pod, error) {
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods by selector %q: %w", selector, err)
	}
	items := append([]corev1.Pod(nil), pods.Items...)
	sortPods(items)
	return items, nil
}

func (c *Client) listPodsBySelectors(ctx context.Context, namespace string, selectors []string) ([]corev1.Pod, string, error) {
	var lastErr error
	for _, selector := range selectors {
		if strings.TrimSpace(selector) == "" {
			continue
		}
		pods, err := c.listPodsBySelector(ctx, namespace, selector)
		if err != nil {
			lastErr = err
			continue
		}
		if len(pods) > 0 {
			return pods, selector, nil
		}
	}
	if lastErr != nil {
		return nil, strings.Join(selectors, " | "), lastErr
	}
	return nil, strings.Join(selectors, " | "), nil
}

func sortPods(pods []corev1.Pod) {
	sort.SliceStable(pods, func(i, j int) bool {
		iTime := pods[i].CreationTimestamp.Time
		jTime := pods[j].CreationTimestamp.Time
		if !iTime.Equal(jTime) {
			return iTime.Before(jTime)
		}
		return pods[i].Name < pods[j].Name
	})
}

func normalizePolicyType(policyType string) string {
	p := strings.ToLower(strings.TrimSpace(policyType))
	switch p {
	case "scale", "scaling", "autoscale", "auto-scaling", "auto_scaling":
		return PolicyTypeAutoscaling
	case "migrate":
		return PolicyTypeMigration
	case "preempt":
		return PolicyTypePreemption
	case "cache":
		return PolicyTypeCaching
	case "loadbalance", "load-balancing", "load_balancing", "loadbalanceing":
		return PolicyTypeLoadBalancing
	case "provision", "preprovisioning", "pre-provisioning":
		return PolicyTypeProvisioning
	default:
		if p == "" {
			return "generic"
		}
		return p
	}
}

func normalizeWorkloadType(workloadType string) string {
	s := strings.ToLower(strings.TrimSpace(workloadType))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, " ", "")

	switch s {
	case "", "auto", "default":
		return WorkloadTypeAuto
	case "deployment", "deploy", "deployments":
		return WorkloadTypeDeployment
	case "statefulset", "sts", "statefulsets":
		return WorkloadTypeStatefulSet
	case "replicaset", "rs", "replicasets":
		return WorkloadTypeReplicaSet
	case "job", "jobs":
		return WorkloadTypeJob
	case "pod", "pods":
		return WorkloadTypePod
	case "workflow", "workflows", "argoworkflow", "argoworkflows":
		return WorkloadTypeWorkflow
	default:
		return workloadType
	}
}

func candidateWorkloadKinds(policyType, requestedKind string) []string {
	if requestedKind != "" && requestedKind != WorkloadTypeAuto {
		return []string{requestedKind}
	}

	switch normalizePolicyType(policyType) {
	case PolicyTypeAutoscaling:
		return []string{WorkloadTypeDeployment, WorkloadTypeStatefulSet, WorkloadTypeReplicaSet}
	case PolicyTypeMigration, PolicyTypePreemption, PolicyTypeCaching, PolicyTypeLoadBalancing, PolicyTypeProvisioning:
		return []string{WorkloadTypeDeployment, WorkloadTypeStatefulSet, WorkloadTypeJob, WorkloadTypePod, WorkloadTypeWorkflow}
	default:
		return []string{WorkloadTypeDeployment, WorkloadTypeStatefulSet, WorkloadTypeJob, WorkloadTypePod, WorkloadTypeWorkflow}
	}
}

func (c *Client) resolveScalableWorkloadKind(ctx context.Context, namespace, name, workloadType string) (string, error) {
	target, err := c.ResolveWorkloadTarget(ctx, namespace, name, workloadType, PolicyTypeAutoscaling, "", false)
	if err != nil {
		return "", err
	}
	switch target.Kind {
	case WorkloadTypeDeployment, WorkloadTypeStatefulSet, WorkloadTypeReplicaSet:
		return target.Kind, nil
	default:
		return "", fmt.Errorf("unsupported scalable workload type: %s", target.Kind)
	}
}

// GetWorkloadReplicas gets the current replica count for a workload.
// workloadType가 auto/empty이면 Deployment -> StatefulSet -> ReplicaSet 순서로 자동 탐색한다.
func (c *Client) GetWorkloadReplicas(ctx context.Context, namespace, name, workloadType string) (int32, error) {
	resolvedKind, err := c.resolveScalableWorkloadKind(ctx, namespace, name, workloadType)
	if err != nil {
		return 0, err
	}

	switch resolvedKind {
	case WorkloadTypeDeployment:
		deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return 0, fmt.Errorf("failed to get deployment: %w", err)
		}
		return deployment.Status.Replicas, nil

	case WorkloadTypeStatefulSet:
		statefulSet, err := c.clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return 0, fmt.Errorf("failed to get statefulset: %w", err)
		}
		return statefulSet.Status.Replicas, nil

	case WorkloadTypeReplicaSet:
		replicaSet, err := c.clientset.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return 0, fmt.Errorf("failed to get replicaset: %w", err)
		}
		return replicaSet.Status.Replicas, nil

	default:
		return 0, fmt.Errorf("unsupported workload type: %s", resolvedKind)
	}
}

// ScaleWorkload scales a workload to the desired number of replicas.
// workloadType가 auto/empty이면 Deployment -> StatefulSet -> ReplicaSet 순서로 자동 탐색한다.
func (c *Client) ScaleWorkload(ctx context.Context, namespace, name, workloadType string, replicas int32) error {
	resolvedKind, err := c.resolveScalableWorkloadKind(ctx, namespace, name, workloadType)
	if err != nil {
		return err
	}

	switch resolvedKind {
	case WorkloadTypeDeployment:
		deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get deployment: %w", err)
		}
		deployment.Spec.Replicas = &replicas
		_, err = c.clientset.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to scale deployment: %w", err)
		}
		return nil

	case WorkloadTypeStatefulSet:
		statefulSet, err := c.clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get statefulset: %w", err)
		}
		statefulSet.Spec.Replicas = &replicas
		_, err = c.clientset.AppsV1().StatefulSets(namespace).Update(ctx, statefulSet, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to scale statefulset: %w", err)
		}
		return nil

	case WorkloadTypeReplicaSet:
		replicaSet, err := c.clientset.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get replicaset: %w", err)
		}
		replicaSet.Spec.Replicas = &replicas
		_, err = c.clientset.AppsV1().ReplicaSets(namespace).Update(ctx, replicaSet, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to scale replicaset: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("unsupported workload type: %s", resolvedKind)
	}
}

// GetWorkloadPodMetrics gets the average CPU, Memory, GPU, and Storage I/O metrics for all pods in a workload.
// Deployment/StatefulSet/Job/Pod/Workflow를 동일 resolver로 해석한다.
func (c *Client) GetWorkloadPodMetrics(ctx context.Context, namespace, workloadName string) (cpuPercent, memoryPercent, gpuPercent int32, storageReadMBps, storageWriteMBps, storageIOPS int64, err error) {
	target, err := c.ResolveWorkloadTarget(ctx, namespace, workloadName, WorkloadTypeAuto, "metrics", "", true)
	if err != nil {
		return 0, 0, 0, 0, 0, 0, err
	}

	var totalCPUPercent, totalMemoryPercent, totalGPUPercent int64
	var totalStorageReadMBps, totalStorageWriteMBps, totalStorageIOPS int64
	podCount := int64(0)

	for _, pod := range target.RunningPods {
		podMetrics, err := c.metricsClientset.MetricsV1beta1().PodMetricses(namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			// metrics-server가 아직 값을 만들지 못한 Pod는 평균에서 제외한다.
			continue
		}

		var podCPUMillis, podMemoryBytes int64
		var podCPURequests, podMemoryRequests int64

		for _, container := range podMetrics.Containers {
			podCPUMillis += container.Usage.Cpu().MilliValue()
			podMemoryBytes += container.Usage.Memory().Value()
		}

		for _, container := range pod.Spec.Containers {
			if cpuReq := container.Resources.Requests.Cpu(); cpuReq != nil {
				podCPURequests += cpuReq.MilliValue()
			}
			if memReq := container.Resources.Requests.Memory(); memReq != nil {
				podMemoryRequests += memReq.Value()
			}
		}

		if podCPURequests > 0 {
			totalCPUPercent += (podCPUMillis * 100) / podCPURequests
		}
		if podMemoryRequests > 0 {
			totalMemoryPercent += (podMemoryBytes * 100) / podMemoryRequests
		}

		gpuPercent := c.calculatePodGPUUtilization(&pod)
		totalGPUPercent += int64(gpuPercent)

		storageRead, storageWrite, iops := c.calculatePodStorageMetrics(ctx, &pod)
		totalStorageReadMBps += storageRead
		totalStorageWriteMBps += storageWrite
		totalStorageIOPS += iops

		podCount++
	}

	if podCount == 0 {
		return 0, 0, 0, 0, 0, 0, NewWaitingForTargetError(namespace, workloadName, target.Kind, "metrics", "no running pods with metrics found")
	}

	avgCPU := int32(totalCPUPercent / podCount)
	avgMemory := int32(totalMemoryPercent / podCount)
	avgGPU := int32(totalGPUPercent / podCount)
	avgStorageRead := totalStorageReadMBps / podCount
	avgStorageWrite := totalStorageWriteMBps / podCount
	avgIOPS := totalStorageIOPS / podCount

	return avgCPU, avgMemory, avgGPU, avgStorageRead, avgStorageWrite, avgIOPS, nil
}

// calculatePodGPUUtilization calculates GPU utilization for a pod
// Attempts to get real GPU metrics via DCGM Exporter, falls back to simulation if unavailable
func (c *Client) calculatePodGPUUtilization(pod *corev1.Pod) int32 {
	// Check if pod has GPU resources requested
	var hasGPU bool

	for _, container := range pod.Spec.Containers {
		// Check for NVIDIA GPU
		if _, exists := container.Resources.Requests["nvidia.com/gpu"]; exists {
			hasGPU = true
			break
		}
		// Check for AMD GPU
		if _, exists := container.Resources.Requests["amd.com/gpu"]; exists {
			hasGPU = true
			break
		}
	}

	if !hasGPU {
		return 0
	}

	// Only try to get metrics for running pods
	if pod.Status.Phase != corev1.PodRunning {
		return 0
	}

	// Try to get real GPU metrics via DCGM Exporter
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	gpuUtil, err := c.getGPUUtilizationFromDCGM(ctx, pod.Namespace, pod.Name)
	if err == nil && gpuUtil >= 0 {
		return gpuUtil
	}

	// Fallback: Simulate GPU utilization if DCGM metrics unavailable
	// This happens when:
	// - DCGM Exporter is not deployed
	// - Pod-level metrics not yet available
	// - Network issues accessing DCGM service
	baseUtil := int32(60)
	variance := int32(rand.New(rand.NewSource(time.Now().UnixNano())).Int31n(30))
	return baseUtil + variance
}

// getGPUUtilizationFromDCGM queries DCGM Exporter for GPU utilization of a specific pod
func (c *Client) getGPUUtilizationFromDCGM(ctx context.Context, namespace, podName string) (int32, error) {
	// DCGM Exporter service endpoint
	dcgmURL := "http://dcgm-exporter.gpu-monitoring.svc.cluster.local:9400/metrics"

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", dcgmURL, nil)
	if err != nil {
		return -1, fmt.Errorf("failed to create DCGM request: %w", err)
	}

	// Execute request
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return -1, fmt.Errorf("failed to query DCGM exporter: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return -1, fmt.Errorf("DCGM exporter returned status %d", resp.StatusCode)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, fmt.Errorf("failed to read DCGM response: %w", err)
	}

	// Parse Prometheus metrics for this specific pod
	// Looking for: DCGM_FI_DEV_GPU_UTIL{namespace="...",pod="..."}
	lines := strings.Split(string(body), "\n")
	var totalUtil float64
	var gpuCount int

	for _, line := range lines {
		// Skip comments and empty lines
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		// Check if this is a GPU utilization metric
		if !strings.HasPrefix(line, "DCGM_FI_DEV_GPU_UTIL{") {
			continue
		}

		// Check if this metric belongs to our pod
		if !strings.Contains(line, fmt.Sprintf("namespace=\"%s\"", namespace)) {
			continue
		}
		if !strings.Contains(line, fmt.Sprintf("pod=\"%s\"", podName)) {
			continue
		}

		// Extract the value (last part after space)
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		value := parts[len(parts)-1]
		util, err := strconv.ParseFloat(value, 64)
		if err != nil {
			continue
		}

		totalUtil += util
		gpuCount++
	}

	if gpuCount == 0 {
		return -1, fmt.Errorf("no GPU metrics found for pod %s/%s", namespace, podName)
	}

	avgUtil := int32(totalUtil / float64(gpuCount))
	return avgUtil, nil
}

// getGPUUtilizationViaNvidiaSMI executes nvidia-smi in the pod to get GPU utilization
// DEPRECATED: Replaced by getGPUUtilizationFromDCGM for better performance and accuracy
func (c *Client) getGPUUtilizationViaNvidiaSMI(ctx context.Context, namespace, podName, containerName string) (int32, error) {
	// nvidia-smi command to get average GPU utilization across all GPUs
	// Format: GPU utilization percentage only
	cmd := []string{
		"nvidia-smi",
		"--query-gpu=utilization.gpu",
		"--format=csv,noheader,nounits",
	}

	// Execute command in pod
	stdout, stderr, err := c.execCommandInPod(ctx, namespace, podName, containerName, cmd)
	if err != nil {
		return -1, fmt.Errorf("failed to execute nvidia-smi: %w (stderr: %s)", err, stderr)
	}

	// Parse output - nvidia-smi returns one line per GPU
	// Example output:
	// 75
	// 82
	// We'll calculate the average
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) == 0 {
		return -1, fmt.Errorf("no GPU utilization data returned")
	}

	var totalUtil int64
	validGPUs := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		util, err := strconv.ParseInt(line, 10, 32)
		if err != nil {
			continue // Skip invalid lines
		}

		totalUtil += util
		validGPUs++
	}

	if validGPUs == 0 {
		return -1, fmt.Errorf("no valid GPU metrics found")
	}

	avgUtil := int32(totalUtil / int64(validGPUs))
	return avgUtil, nil
}

// execCommandInPod executes a command in a container and returns stdout, stderr, and error
func (c *Client) execCommandInPod(ctx context.Context, namespace, podName, containerName string, command []string) (string, string, error) {
	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec")

	// PodExecOptions 는 core v1 전용 쿼리로 직렬화되어야 하며, metav1.ParameterCodec 은
	// 해당 타입이 등록돼 있지 않아 URL 에 stdout/stderr 가 누락될 수 있다(업그레이드 실패).
	req.VersionedParams(&corev1.PodExecOptions{
		Container: containerName,
		Command:   command,
		Stdout:    true,
		Stderr:    true,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.config, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("failed to create executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	return stdout.String(), stderr.String(), err
}

// FileExistsInPod은 지정 컨테이너에서 absPath가 일반 파일로 존재하는지 확인한다.
//
// exec 비정상 종료는 파일 없음으로 간주한다(네트워크 오류와 구분하지 않음).
func (c *Client) FileExistsInPod(ctx context.Context, namespace, podName, containerName, absPath string) (bool, error) {
	_, _, err := c.execCommandInPod(ctx, namespace, podName, containerName, []string{"test", "-f", absPath})
	if err == nil {
		return true, nil
	}
	return false, nil
}

// TouchPathInPod은 absPath의 부모 디렉터리를 만든 뒤 빈 파일을 생성한다.
//
// output stage의 _SUCCESS 마커 등, Pod 파일시스템에 완료 표식을 남길 때 사용한다.
func (c *Client) TouchPathInPod(ctx context.Context, namespace, podName, containerName, absPath string) error {
	dir := filepath.Dir(absPath)
	_, stderr, err := c.execCommandInPod(ctx, namespace, podName, containerName, []string{"mkdir", "-p", dir})
	if err != nil {
		return fmt.Errorf("mkdir in pod: %w (stderr=%s)", err, strings.TrimSpace(stderr))
	}
	_, stderr, err = c.execCommandInPod(ctx, namespace, podName, containerName, []string{"touch", absPath})
	if err != nil {
		return fmt.Errorf("touch in pod: %w (stderr=%s)", err, strings.TrimSpace(stderr))
	}
	return nil
}

// GetPodGPUMetrics attempts to get real GPU metrics from custom metrics API
// This is a placeholder for future implementation with DCGM or custom metrics
func (c *Client) GetPodGPUMetrics(ctx context.Context, namespace, podName string) (float64, error) {
	// TODO: Implement custom metrics API query
	// Example query for DCGM metrics:
	// customMetrics := c.customMetricsClient.NamespacedMetrics(namespace)
	// metrics, err := customMetrics.GetForObject(schema.GroupKind{Kind: "Pod"}, podName, "gpu_utilization")

	return 0.0, fmt.Errorf("custom GPU metrics not implemented yet")
}

// ============================================================================
// Loadbalancing Operations
// ============================================================================

// ListNodes returns a list of all node names in the cluster
func (c *Client) ListNodes(ctx context.Context) ([]string, error) {
	nodeList, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	nodeNames := make([]string, 0, len(nodeList.Items))
	for _, node := range nodeList.Items {
		nodeNames = append(nodeNames, node.Name)
	}

	return nodeNames, nil
}

// GetNodeMetrics gets CPU and Memory utilization percentage for a node
func (c *Client) GetNodeMetrics(ctx context.Context, nodeName string) (cpuPercent, memoryPercent int32, err error) {
	// Get node metrics from metrics-server
	nodeMetrics, err := c.metricsClientset.MetricsV1beta1().NodeMetricses().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get node metrics: %w", err)
	}

	// Get node capacity
	node, err := c.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get node: %w", err)
	}

	// Calculate CPU percentage
	cpuUsage := nodeMetrics.Usage.Cpu().MilliValue()
	cpuCapacity := node.Status.Allocatable.Cpu().MilliValue()
	if cpuCapacity > 0 {
		cpuPercent = int32(float64(cpuUsage) / float64(cpuCapacity) * 100)
	}

	// Calculate Memory percentage
	memUsage := nodeMetrics.Usage.Memory().Value()
	memCapacity := node.Status.Allocatable.Memory().Value()
	if memCapacity > 0 {
		memoryPercent = int32(float64(memUsage) / float64(memCapacity) * 100)
	}

	return cpuPercent, memoryPercent, nil
}

// GetNodeCapacity returns the total capacity of a node
func (c *Client) GetNodeCapacity(ctx context.Context, nodeName string) (cpuCapacity, memoryCapacity string, gpuCapacity int32, err error) {
	node, err := c.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to get node: %w", err)
	}

	// Get CPU capacity
	cpu := node.Status.Allocatable.Cpu()
	cpuCapacity = cpu.String()

	// Get Memory capacity
	memory := node.Status.Allocatable.Memory()
	memoryCapacity = memory.String()

	// Get GPU capacity
	gpuResource := node.Status.Allocatable["nvidia.com/gpu"]
	gpuCapacity = int32(gpuResource.Value())

	return cpuCapacity, memoryCapacity, gpuCapacity, nil
}

// GetNodePodCount returns the number of pods running on a node
func (c *Client) GetNodePodCount(ctx context.Context, nodeName string) (int32, error) {
	fieldSelector := fields.OneTermEqualSelector("spec.nodeName", nodeName).String()
	podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to list pods on node: %w", err)
	}

	// Count only running and pending pods
	count := int32(0)
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodPending {
			count++
		}
	}

	return count, nil
}

// GetNodeLabel returns the value of a specific label on a node
func (c *Client) GetNodeLabel(ctx context.Context, nodeName string, labelKey string) (string, error) {
	node, err := c.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get node: %w", err)
	}

	value, exists := node.Labels[labelKey]
	if !exists {
		return "", fmt.Errorf("label %s not found on node %s", labelKey, nodeName)
	}

	return value, nil
}

// GetNodeGPUUtilization returns GPU utilization percentage for a node
func (c *Client) GetNodeGPUUtilization(ctx context.Context, nodeName string) (int32, error) {
	// Query DCGM Exporter for node-level GPU metrics
	dcgmURL := "http://dcgm-exporter.gpu-monitoring.svc.cluster.local:9400/metrics"

	req, err := http.NewRequestWithContext(ctx, "GET", dcgmURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create DCGM request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to query DCGM exporter: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("DCGM exporter returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read DCGM response: %w", err)
	}

	// Parse Prometheus metrics for this node
	lines := strings.Split(string(body), "\n")
	var totalUtil float64
	var gpuCount int

	for _, line := range lines {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		if !strings.HasPrefix(line, "DCGM_FI_DEV_GPU_UTIL{") {
			continue
		}

		// Check if this metric belongs to our node
		if !strings.Contains(line, fmt.Sprintf("node=\"%s\"", nodeName)) {
			continue
		}

		// Extract the value
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		value := parts[len(parts)-1]
		util, err := strconv.ParseFloat(value, 64)
		if err != nil {
			continue
		}

		totalUtil += util
		gpuCount++
	}

	if gpuCount == 0 {
		// No GPU metrics found - node might not have GPUs
		return 0, nil
	}

	avgUtil := int32(totalUtil / float64(gpuCount))
	return avgUtil, nil
}

// GetNodeStorageMetrics retrieves Storage I/O metrics for a node
// Returns readMBps, writeMBps, iops, and utilization percentage
func (c *Client) GetNodeStorageMetrics(ctx context.Context, nodeName string) (readMBps, writeMBps, iops int64, utilization int32, err error) {
	// Query node_exporter or storage metrics provider for I/O metrics
	// This could be node_exporter, custom CSD exporter, or Prometheus
	nodeExporterURL := fmt.Sprintf("http://%s:9100/metrics", nodeName)

	req, err := http.NewRequestWithContext(ctx, "GET", nodeExporterURL, nil)
	if err != nil {
		// Fall back to simulated metrics if node_exporter is not available
		return c.getSimulatedStorageMetrics(nodeName)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Fall back to simulated metrics
		return c.getSimulatedStorageMetrics(nodeName)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.getSimulatedStorageMetrics(nodeName)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return c.getSimulatedStorageMetrics(nodeName)
	}

	// Parse Prometheus metrics for storage I/O
	lines := strings.Split(string(body), "\n")
	var totalReadBytes, totalWriteBytes float64
	var readOps, writeOps float64

	for _, line := range lines {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		// Parse node_disk_read_bytes_total
		if strings.HasPrefix(line, "node_disk_read_bytes_total{") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if val, e := strconv.ParseFloat(parts[len(parts)-1], 64); e == nil {
					totalReadBytes += val
				}
			}
		}

		// Parse node_disk_written_bytes_total
		if strings.HasPrefix(line, "node_disk_written_bytes_total{") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if val, e := strconv.ParseFloat(parts[len(parts)-1], 64); e == nil {
					totalWriteBytes += val
				}
			}
		}

		// Parse node_disk_reads_completed_total
		if strings.HasPrefix(line, "node_disk_reads_completed_total{") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if val, e := strconv.ParseFloat(parts[len(parts)-1], 64); e == nil {
					readOps += val
				}
			}
		}

		// Parse node_disk_writes_completed_total
		if strings.HasPrefix(line, "node_disk_writes_completed_total{") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if val, e := strconv.ParseFloat(parts[len(parts)-1], 64); e == nil {
					writeOps += val
				}
			}
		}
	}

	// Convert to MB/s (assuming 1-second sampling interval for rate calculation)
	// In production, this should use rate calculation from Prometheus
	readMBps = int64(totalReadBytes / (1024 * 1024))
	writeMBps = int64(totalWriteBytes / (1024 * 1024))
	iops = int64(readOps + writeOps)

	// Calculate utilization as percentage (simplified)
	// In production, use node_disk_io_time_seconds_total for accurate utilization
	utilization = int32(min(100, (readMBps+writeMBps)/10)) // Rough estimate

	return readMBps, writeMBps, iops, utilization, nil
}

// getSimulatedStorageMetrics returns simulated storage metrics for testing
func (c *Client) getSimulatedStorageMetrics(nodeName string) (readMBps, writeMBps, iops int64, utilization int32, err error) {
	// Generate deterministic but varied metrics based on node name
	hash := 0
	for _, ch := range nodeName {
		hash += int(ch)
	}

	// Simulate realistic storage I/O metrics
	readMBps = int64(50 + (hash % 200))   // 50-250 MB/s
	writeMBps = int64(20 + (hash % 100))  // 20-120 MB/s
	iops = int64(1000 + (hash % 3000))    // 1000-4000 IOPS
	utilization = int32(20 + (hash % 60)) // 20-80%

	return readMBps, writeMBps, iops, utilization, nil
}

// ListPodsOnNode returns a list of pod names running on a specific node
func (c *Client) ListPodsOnNode(ctx context.Context, nodeName string) ([]types.PodRef, error) {
	fieldSelector := fields.OneTermEqualSelector("spec.nodeName", nodeName).String()
	podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods on node: %w", err)
	}

	pods := make([]types.PodRef, 0)
	for _, pod := range podList.Items {
		// Only include running pods
		if pod.Status.Phase == corev1.PodRunning {
			pods = append(pods, types.PodRef{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			})
		}
	}

	return pods, nil
}

// calculatePodStorageMetrics calculates storage I/O metrics for a pod
// Returns: (read throughput MB/s, write throughput MB/s, IOPS)
// For AI/ML workloads, storage I/O is critical for data loading performance
func (c *Client) calculatePodStorageMetrics(ctx context.Context, pod *corev1.Pod) (readMBps, writeMBps, iops int64) {
	// Try to get real storage metrics from node exporter or cAdvisor
	// Priority 1: Query Prometheus node-exporter for container-level disk I/O
	realRead, realWrite, realIOPS, err := c.getStorageMetricsFromPrometheus(ctx, pod.Namespace, pod.Name)
	if err == nil && (realRead > 0 || realWrite > 0 || realIOPS > 0) {
		return realRead, realWrite, realIOPS
	}

	// Priority 2: Estimate from PVC usage patterns
	estimatedRead, estimatedWrite, estimatedIOPS := c.estimateStorageMetricsFromPVC(ctx, pod)
	if estimatedRead > 0 || estimatedWrite > 0 || estimatedIOPS > 0 {
		return estimatedRead, estimatedWrite, estimatedIOPS
	}

	// Fallback: Simulate storage metrics for AI/ML workloads
	// AI training typically has high read throughput (dataset loading)
	// and moderate write throughput (checkpoints, logs)

	// Check if pod has PVCs (indicates data-intensive workload)
	hasPVC := false
	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			hasPVC = true
			break
		}
	}

	if hasPVC {
		// Simulate typical AI training I/O pattern
		// Read: 200-800 MB/s (dataset streaming)
		// Write: 50-200 MB/s (checkpoint saving)
		// IOPS: 1000-5000 (mixed read/write)
		baseRead := int64(400 + rand.New(rand.NewSource(time.Now().UnixNano())).Int63n(400))
		baseWrite := int64(100 + rand.New(rand.NewSource(time.Now().UnixNano())).Int63n(100))
		baseIOPS := int64(2000 + rand.New(rand.NewSource(time.Now().UnixNano())).Int63n(3000))

		return baseRead, baseWrite, baseIOPS
	}

	// No PVC - minimal storage I/O
	return 0, 0, 0
}

// getStorageMetricsFromPrometheus queries Prometheus for container disk I/O metrics
// Uses node-exporter or cAdvisor metrics: container_fs_reads_bytes_total, container_fs_writes_bytes_total
func (c *Client) getStorageMetricsFromPrometheus(ctx context.Context, namespace, podName string) (readMBps, writeMBps, iops int64, err error) {
	// Prometheus endpoint (assumes prometheus-server in monitoring namespace)
	promURL := "http://prometheus-server.monitoring.svc.cluster.local:9090/api/v1/query"

	// Query for read throughput (bytes/sec over last 1 minute)
	readQuery := fmt.Sprintf(`rate(container_fs_reads_bytes_total{namespace="%s",pod="%s"}[1m])`, namespace, podName)
	readBytes, err := c.queryPrometheus(ctx, promURL, readQuery)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to query read metrics: %w", err)
	}

	// Query for write throughput
	writeQuery := fmt.Sprintf(`rate(container_fs_writes_bytes_total{namespace="%s",pod="%s"}[1m])`, namespace, podName)
	writeBytes, err := c.queryPrometheus(ctx, promURL, writeQuery)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to query write metrics: %w", err)
	}

	// Query for IOPS (reads + writes per second)
	iopsQuery := fmt.Sprintf(`rate(container_fs_reads_total{namespace="%s",pod="%s"}[1m]) + rate(container_fs_writes_total{namespace="%s",pod="%s"}[1m])`,
		namespace, podName, namespace, podName)
	iopsValue, err := c.queryPrometheus(ctx, promURL, iopsQuery)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to query IOPS metrics: %w", err)
	}

	// Convert bytes/sec to MB/sec
	readMBps = int64(readBytes / (1024 * 1024))
	writeMBps = int64(writeBytes / (1024 * 1024))
	iops = int64(iopsValue)

	return readMBps, writeMBps, iops, nil
}

// queryPrometheus executes a PromQL query and returns the result value
func (c *Client) queryPrometheus(ctx context.Context, promURL, query string) (float64, error) {
	// Create HTTP request with query parameter
	req, err := http.NewRequestWithContext(ctx, "GET", promURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	q := req.URL.Query()
	q.Add("query", query)
	req.URL.RawQuery = q.Encode()

	// Execute request
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to query Prometheus: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Prometheus returned status %d", resp.StatusCode)
	}

	// Parse JSON response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	// Simple parsing: look for "value":[timestamp, "value"]
	// Full JSON parsing would be better, but this is a fallback
	valueStr := string(body)
	if strings.Contains(valueStr, `"result":[]`) {
		return 0, fmt.Errorf("no metrics found")
	}

	// Extract numeric value (simplified parsing)
	// Real implementation should use proper JSON unmarshaling
	startIdx := strings.LastIndex(valueStr, `"value":[`)
	if startIdx == -1 {
		return 0, fmt.Errorf("invalid response format")
	}

	endIdx := strings.Index(valueStr[startIdx:], "]")
	if endIdx == -1 {
		return 0, fmt.Errorf("invalid response format")
	}

	valuesPart := valueStr[startIdx+9 : startIdx+endIdx]
	parts := strings.Split(valuesPart, ",")
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid value format")
	}

	valueNumStr := strings.Trim(parts[1], `" `)
	value, err := strconv.ParseFloat(valueNumStr, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse value: %w", err)
	}

	return value, nil
}

// ============================================================================
// Preemption Operations
// ============================================================================

// GetPodResourceInfo retrieves resource requests and priority information for a pod
// Includes Storage I/O metrics for AI/ML workload preemption decisions
func (c *Client) GetPodResourceInfo(ctx context.Context, namespace, name string) (*types.PodResourceInfo, error) {
	pod, err := c.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}

	info := &types.PodResourceInfo{
		PodName:       name,
		PodNamespace:  namespace,
		CreationTime:  pod.CreationTimestamp.Time,
		PriorityValue: 0,
		PriorityClass: "",
	}

	// Get priority information
	if pod.Spec.Priority != nil {
		info.PriorityValue = *pod.Spec.Priority
	}
	if pod.Spec.PriorityClassName != "" {
		info.PriorityClass = pod.Spec.PriorityClassName
	}

	// Calculate total resource requests
	var totalCPU, totalMemory int64
	var totalGPU int32

	for _, container := range pod.Spec.Containers {
		// CPU requests
		if cpuReq := container.Resources.Requests.Cpu(); cpuReq != nil {
			totalCPU += cpuReq.MilliValue()
		}

		// Memory requests
		if memReq := container.Resources.Requests.Memory(); memReq != nil {
			totalMemory += memReq.Value()
		}

		// GPU requests (NVIDIA)
		if gpuReq, exists := container.Resources.Requests["nvidia.com/gpu"]; exists {
			totalGPU += int32(gpuReq.Value())
		}

		// GPU requests (AMD)
		if gpuReq, exists := container.Resources.Requests["amd.com/gpu"]; exists {
			totalGPU += int32(gpuReq.Value())
		}
	}

	info.CPURequest = totalCPU
	info.MemoryRequest = totalMemory
	info.GPURequest = totalGPU

	// Get Storage I/O metrics for AI/ML workload preemption
	// This is critical for storage-aware scheduling decisions
	storageRead, storageWrite, storageIOPS := c.calculatePodStorageMetrics(ctx, pod)
	info.StorageReadMBps = storageRead
	info.StorageWriteMBps = storageWrite
	info.StorageIOPS = storageIOPS

	// Calculate PVC information
	var pvcCount int32
	var totalPVCSize int64
	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			pvcCount++
			pvcName := vol.PersistentVolumeClaim.ClaimName
			pvc, err := c.clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
			if err == nil {
				if storageSize, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
					totalPVCSize += storageSize.Value()
				}
			}
		}
	}
	info.PVCCount = pvcCount
	info.TotalPVCSize = totalPVCSize

	return info, nil
}

// EvictPod evicts a pod using the Kubernetes Eviction API
func (c *Client) EvictPod(ctx context.Context, namespace, name string, gracePeriodSeconds int64) error {
	// Create eviction object
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		DeleteOptions: &metav1.DeleteOptions{
			GracePeriodSeconds: &gracePeriodSeconds,
		},
	}

	// Execute eviction
	err := c.clientset.PolicyV1().Evictions(namespace).Evict(ctx, eviction)
	if err != nil {
		return fmt.Errorf("failed to evict pod %s/%s: %w", namespace, name, err)
	}

	return nil
}

// estimateStorageMetricsFromPVC estimates storage I/O based on PVC size and age
// Assumes larger/older PVCs have higher I/O activity for AI/ML data loading
func (c *Client) estimateStorageMetricsFromPVC(ctx context.Context, pod *corev1.Pod) (readMBps, writeMBps, iops int64) {
	totalPVCSize := int64(0)
	pvcCount := 0

	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim == nil {
			continue
		}

		pvcName := vol.PersistentVolumeClaim.ClaimName
		pvc, err := c.clientset.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err != nil {
			continue
		}

		// Get PVC storage size
		if storageSize, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			totalPVCSize += storageSize.Value()
			pvcCount++
		}
	}

	if pvcCount == 0 {
		return 0, 0, 0
	}

	// Estimate I/O based on PVC size
	// Assumption: Larger datasets = more I/O activity
	// 100GB PVC ≈ 200 MB/s read, 50 MB/s write
	// 1TB PVC ≈ 500 MB/s read, 150 MB/s write
	totalGB := totalPVCSize / (1024 * 1024 * 1024)

	if totalGB > 1000 {
		totalGB = 1000 // Cap at 1TB
	}

	readMBps = 100 + (totalGB * 40 / 100) // 100-500 MB/s
	writeMBps = 30 + (totalGB * 12 / 100) // 30-150 MB/s
	iops = 1000 + (totalGB * 100 / 100)   // 1000-2000 IOPS

	return readMBps, writeMBps, iops
}

type cachePrefetchPodResult struct {
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
	Error string `json:"error,omitempty"`
}

// RunCachePrefetchPod은 source PVC를 마운트한 임시 Pod에서 sourcePath 하위 파일을 스캔해 로드 크기를 계산한다.
func (c *Client) RunCachePrefetchPod(ctx context.Context, namespace, pvcName, sourcePath, jobID string) (int64, int64, error) {
	trimmedJobID := strings.ToLower(strings.ReplaceAll(jobID, "_", "-"))
	if trimmedJobID == "" {
		trimmedJobID = fmt.Sprintf("%d", time.Now().Unix())
	}
	if len(trimmedJobID) > 32 {
		trimmedJobID = trimmedJobID[:32]
	}
	podName := fmt.Sprintf("cache-prefetch-%s", trimmedJobID)

	if sourcePath == "" || sourcePath == "." {
		sourcePath = "/data"
	}

	script := strings.Join([]string{
		"import json, os, sys",
		`root = os.path.normpath(sys.argv[1] if len(sys.argv) > 1 else "/data")`,
		"if not os.path.exists(root):",
		`  print(json.dumps({"error": f"source path not found: {root}"}))`,
		"  raise SystemExit(2)",
		"total = 0",
		"files = 0",
		"for dirpath, _, filenames in os.walk(root):",
		"  for name in filenames:",
		"    path = os.path.join(dirpath, name)",
		"    if os.path.isfile(path):",
		"      files += 1",
		"      total += os.path.getsize(path)",
		"if files == 0:",
		`  print(json.dumps({"error": f"no regular files under {root}"}))`,
		"  raise SystemExit(3)",
		`print(json.dumps({"bytes": total, "files": files}))`,
	}, "\n")

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":                             "ai-storage-orchestrator",
				"component":                       "cache-prefetch",
				"ai-storage.orchestrator/cacheID": jobID,
			},
			// WHY: cache-prefetch Pod 은 main container(prefetch) 가 종료되면 즉시 정리해야
			//      한다. 클러스터의 insight-trace mutating webhook 이 사이드카를 주입할 때
			//      Pod annotation 으로 main container 를 식별한다. 아래 세 키를 모두 박아
			//      ① insight-trace 의 자동 종료 로직(mlops.keti.io/main-container),
			//      ② demo 표준 키(insight-trace.keti.io/main-container) 와 호환되게 한다.
			Annotations: map[string]string{
				"insight-trace.keti.io/main-container": "prefetch",
				"mlops.keti.io/main-container":         "prefetch",
				"workload.keti.io/main-container":      "prefetch",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			// WHY: insight-trace 사이드카가 Kubernetes API 로 자기 Pod 상태를 polling 해서
			//      main container 종료 시 자신을 끝낸다. default SA 는 자기 Pod GET 권한이
			//      없어 polling 이 매번 401/403 으로 실패하고 사이드카가 영원히 살아남아
			//      Pod 이 Completed 되지 못한다. orchestrator 의 SA(권한 보유) 를 위임한다.
			ServiceAccountName: "ai-storage-orchestrator",
			Containers: []corev1.Container{
				{
					Name:            "prefetch",
					Image:           "python:3.11-slim",
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command: []string{
						"python3", "-c", script, sourcePath,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "source-pvc",
							MountPath: "/data",
							ReadOnly:  true,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "source-pvc",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
							ReadOnly:  true,
						},
					},
				},
			},
		},
	}

	if _, err := c.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return 0, 0, fmt.Errorf("create cache-prefetch pod %s/%s: %w", namespace, podName, err)
	}
	logPrefix := fmt.Sprintf("cache-prefetch pod %s/%s", namespace, podName)
	defer func() {
		_ = c.clientset.CoreV1().Pods(namespace).Delete(context.Background(), podName, metav1.DeleteOptions{})
	}()

	waitCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()

	for {
		select {
		case <-waitCtx.Done():
			return 0, 0, fmt.Errorf("%s timeout: %w", logPrefix, waitCtx.Err())
		case <-time.After(2 * time.Second):
		}

		current, err := c.clientset.CoreV1().Pods(namespace).Get(waitCtx, podName, metav1.GetOptions{})
		if err != nil {
			return 0, 0, fmt.Errorf("get %s: %w", logPrefix, err)
		}

		// WHY: insight-trace sidecar 가 prefetch 종료 후에도 살아남으면 Pod.Phase 가 영원히
		//      Running 으로 머무른다. main container 단위 Terminated 만으로 결과를 결정해
		//      sidecar 의 lifecycle 과 무관하게 cache 정책이 PASS 되도록 한다.
		if isCachePrefetchContainerTerminated(current) {
			result, err := readCachePrefetchResult(waitCtx, c, namespace, podName, current, logPrefix)
			if err != nil {
				return 0, 0, err
			}
			return result.Bytes, result.Files, nil
		}

		switch current.Status.Phase {
		case corev1.PodSucceeded, corev1.PodFailed:
			var logText string
			logErrs := make([]string, 0, 3)
			for i := 0; i < 3; i++ {
				logReq := c.clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{Container: "prefetch"})
				rawLogs, logErr := logReq.Do(waitCtx).Raw()
				if logErr == nil {
					logText = strings.TrimSpace(string(rawLogs))
					if logText != "" {
						break
					}
					logErrs = append(logErrs, "empty logs")
				} else {
					logErrs = append(logErrs, logErr.Error())
					// Pod가 이미 정리됐더라도 직전 terminal 상태를 기준으로 fallback 판단 가능
					if apierrors.IsNotFound(logErr) {
						break
					}
				}
				select {
				case <-waitCtx.Done():
					break
				case <-time.After(800 * time.Millisecond):
				}
			}

			if logText != "" {
				lines := strings.Split(logText, "\n")
				last := strings.TrimSpace(lines[len(lines)-1])

				var result cachePrefetchPodResult
				if err := json.Unmarshal([]byte(last), &result); err == nil {
					if result.Error != "" {
						return 0, 0, fmt.Errorf("%s error: %s", logPrefix, result.Error)
					}
					if current.Status.Phase == corev1.PodFailed {
						return 0, 0, fmt.Errorf("%s failed without explicit error", logPrefix)
					}
					return result.Bytes, result.Files, nil
				}

				if current.Status.Phase == corev1.PodFailed {
					return 0, 0, fmt.Errorf("%s failed: %s", logPrefix, logText)
				}
			}

			// 로그 회수 실패 시 ContainerStatuses 종료 정보를 fallback으로 사용한다.
			for _, cs := range current.Status.ContainerStatuses {
				if cs.Name != "prefetch" || cs.State.Terminated == nil {
					continue
				}
				t := cs.State.Terminated
				if t.ExitCode == 0 {
					return 0, 0, nil
				}
				return 0, 0, fmt.Errorf("%s terminated(exit=%d reason=%s message=%s logs=%s)",
					logPrefix, t.ExitCode, t.Reason, strings.TrimSpace(t.Message), strings.Join(logErrs, " | "))
			}
			return 0, 0, fmt.Errorf("%s terminated but no logs/status details (logErrors=%s)", logPrefix, strings.Join(logErrs, " | "))
		}
	}
}

// isCachePrefetchContainerTerminated 는 cache-prefetch Pod 의 main container(prefetch)가
// 종료되었는지만 본다. Sidecar 컨테이너(insight-trace 등) 의 살아있음 여부는 무시한다.
func isCachePrefetchContainerTerminated(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == "prefetch" && cs.State.Terminated != nil {
			return true
		}
	}
	return false
}

// readCachePrefetchResult 는 prefetch 컨테이너가 Terminated 된 시점에 로그 또는 종료 코드를
// 기반으로 cache-prefetch 결과를 해석한다.
func readCachePrefetchResult(ctx context.Context, c *Client, namespace, podName string, current *corev1.Pod, logPrefix string) (*cachePrefetchPodResult, error) {
	// 로그 1차 시도: prefetch 컨테이너의 stdout
	logReq := c.clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{Container: "prefetch"})
	rawLogs, logErr := logReq.Do(ctx).Raw()
	logText := strings.TrimSpace(string(rawLogs))

	if logText != "" {
		lines := strings.Split(logText, "\n")
		last := strings.TrimSpace(lines[len(lines)-1])
		var result cachePrefetchPodResult
		if jerr := json.Unmarshal([]byte(last), &result); jerr == nil {
			if result.Error != "" {
				return nil, fmt.Errorf("%s error: %s", logPrefix, result.Error)
			}
			return &result, nil
		}
	}

	// fallback: 종료 코드만 본다.
	for _, cs := range current.Status.ContainerStatuses {
		if cs.Name != "prefetch" || cs.State.Terminated == nil {
			continue
		}
		t := cs.State.Terminated
		if t.ExitCode == 0 {
			return &cachePrefetchPodResult{}, nil
		}
		return nil, fmt.Errorf("%s prefetch container terminated exit=%d reason=%s message=%s logErr=%v",
			logPrefix, t.ExitCode, t.Reason, strings.TrimSpace(t.Message), logErr)
	}
	return nil, fmt.Errorf("%s prefetch terminated but no logs/status details (logErr=%v)", logPrefix, logErr)
}
