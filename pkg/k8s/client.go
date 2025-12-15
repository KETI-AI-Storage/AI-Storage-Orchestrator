package k8s

import (
	"context"
	"fmt"
	"time"

	"ai-storage-orchestrator/pkg/types"
	
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Client wraps Kubernetes client with migration-specific functionality
type Client struct {
	clientset       kubernetes.Interface
	metricsClientset metricsclientset.Interface
	config          *rest.Config
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

// GetPod retrieves a pod by name and namespace
func (c *Client) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	return c.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
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

// CreatePersistentVolumeClaim creates a PVC for checkpointing container state
func (c *Client) CreatePersistentVolumeClaim(ctx context.Context, namespace, name string, size string) error {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app":       "ai-storage-orchestrator",
				"component": "migration-checkpoint",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(size),
				},
			},
		},
	}

	_, err := c.clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	return err
}

// DeletePod deletes a pod gracefully
func (c *Client) DeletePod(ctx context.Context, namespace, name string) error {
	gracePeriod := int64(30) // 30 seconds grace period
	
	return c.clientset.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
}

// CreateOptimizedPod creates a new pod with only running containers
func (c *Client) CreateOptimizedPod(ctx context.Context, originalPod *corev1.Pod, targetNode string, containerStates []types.ContainerState, checkpointPVC string) (*corev1.Pod, error) {
	// Create new pod spec based on original but optimized
	newPod := originalPod.DeepCopy()
	
	// Clear status and metadata that should not be copied
	newPod.Status = corev1.PodStatus{}
	newPod.ObjectMeta = metav1.ObjectMeta{
		Name:      fmt.Sprintf("%s-migrated-%d", originalPod.Name, time.Now().Unix()),
		Namespace: originalPod.Namespace,
		Labels:    originalPod.Labels,
	}
	
	// Add migration labels
	if newPod.Labels == nil {
		newPod.Labels = make(map[string]string)
	}
	newPod.Labels["migration.ai-storage/original-pod"] = originalPod.Name
	newPod.Labels["migration.ai-storage/target-node"] = targetNode
	
	// Set node selector for target node
	newPod.Spec.NodeName = targetNode
	
	// Filter containers - only include those that should be migrated
	var optimizedContainers []corev1.Container
	for _, container := range newPod.Spec.Containers {
		for _, state := range containerStates {
			if container.Name == state.Name && state.ShouldMigrate {
				// Add checkpoint volume mount if specified
				if checkpointPVC != "" {
					container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
						Name:      "checkpoint-volume",
						MountPath: "/migration-checkpoint",
					})
				}
				optimizedContainers = append(optimizedContainers, container)
				break
			}
		}
	}
	
	newPod.Spec.Containers = optimizedContainers
	
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
	}

	return c.clientset.CoreV1().Pods(newPod.Namespace).Create(ctx, newPod, metav1.CreateOptions{})
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
		
		totalCPU += cpu.MilliValue()     // Convert to millicores
		totalMemory += memory.Value()    // Bytes
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

// GetWorkloadReplicas gets the current replica count for a workload (Deployment, StatefulSet, ReplicaSet)
func (c *Client) GetWorkloadReplicas(ctx context.Context, namespace, name, workloadType string) (int32, error) {
	switch workloadType {
	case "Deployment":
		deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return 0, fmt.Errorf("failed to get deployment: %w", err)
		}
		return deployment.Status.Replicas, nil

	case "StatefulSet":
		statefulSet, err := c.clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return 0, fmt.Errorf("failed to get statefulset: %w", err)
		}
		return statefulSet.Status.Replicas, nil

	case "ReplicaSet":
		replicaSet, err := c.clientset.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return 0, fmt.Errorf("failed to get replicaset: %w", err)
		}
		return replicaSet.Status.Replicas, nil

	default:
		return 0, fmt.Errorf("unsupported workload type: %s", workloadType)
	}
}

// ScaleWorkload scales a workload to the desired number of replicas
func (c *Client) ScaleWorkload(ctx context.Context, namespace, name, workloadType string, replicas int32) error {
	switch workloadType {
	case "Deployment":
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

	case "StatefulSet":
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

	case "ReplicaSet":
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
		return fmt.Errorf("unsupported workload type: %s", workloadType)
	}
}

// GetWorkloadPodMetrics gets the average CPU, Memory, and GPU utilization for all pods in a workload
func (c *Client) GetWorkloadPodMetrics(ctx context.Context, namespace, workloadName string) (cpuPercent, memoryPercent, gpuPercent int32, err error) {
	// List pods with label selector matching the workload
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", workloadName),
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return 0, 0, 0, fmt.Errorf("no pods found for workload %s", workloadName)
	}

	var totalCPUPercent, totalMemoryPercent, totalGPUPercent int64
	podCount := int64(0)

	for _, pod := range pods.Items {
		// Skip pods that are not running
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}

		// Get pod metrics
		podMetrics, err := c.metricsClientset.MetricsV1beta1().PodMetricses(namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			// If metrics not available for this pod, skip it
			continue
		}

		// Calculate resource usage for this pod
		var podCPUMillis, podMemoryBytes int64
		var podCPURequests, podMemoryRequests int64

		for _, container := range podMetrics.Containers {
			podCPUMillis += container.Usage.Cpu().MilliValue()
			podMemoryBytes += container.Usage.Memory().Value()
		}

		// Get resource requests from pod spec
		for _, container := range pod.Spec.Containers {
			if cpuReq := container.Resources.Requests.Cpu(); cpuReq != nil {
				podCPURequests += cpuReq.MilliValue()
			}
			if memReq := container.Resources.Requests.Memory(); memReq != nil {
				podMemoryRequests += memReq.Value()
			}
		}

		// Calculate percentage (usage / requests * 100)
		if podCPURequests > 0 {
			totalCPUPercent += (podCPUMillis * 100) / podCPURequests
		}
		if podMemoryRequests > 0 {
			totalMemoryPercent += (podMemoryBytes * 100) / podMemoryRequests
		}

		// GPU metrics would need custom metrics from device plugins
		// For now, return 0 for GPU
		totalGPUPercent += 0

		podCount++
	}

	if podCount == 0 {
		return 0, 0, 0, fmt.Errorf("no running pods with metrics found for workload %s", workloadName)
	}

	// Calculate average
	avgCPU := int32(totalCPUPercent / podCount)
	avgMemory := int32(totalMemoryPercent / podCount)
	avgGPU := int32(totalGPUPercent / podCount)

	return avgCPU, avgMemory, avgGPU, nil
}
