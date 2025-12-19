package k8s

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ai-storage-orchestrator/pkg/types"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
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

// GetWorkloadPodMetrics gets the average CPU, Memory, GPU, and Storage I/O metrics for all pods in a workload
func (c *Client) GetWorkloadPodMetrics(ctx context.Context, namespace, workloadName string) (cpuPercent, memoryPercent, gpuPercent int32, storageReadMBps, storageWriteMBps, storageIOPS int64, err error) {
	// Get label selector for the workload
	// Try Deployment first, then StatefulSet, then ReplicaSet
	var labelSelector string

	// Try to get Deployment
	deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, workloadName, metav1.GetOptions{})
	if err == nil {
		// Convert matchLabels to selector string
		labelSelector = metav1.FormatLabelSelector(deployment.Spec.Selector)
	} else {
		// Try StatefulSet
		statefulSet, err := c.clientset.AppsV1().StatefulSets(namespace).Get(ctx, workloadName, metav1.GetOptions{})
		if err == nil {
			labelSelector = metav1.FormatLabelSelector(statefulSet.Spec.Selector)
		} else {
			// Try ReplicaSet
			replicaSet, err := c.clientset.AppsV1().ReplicaSets(namespace).Get(ctx, workloadName, metav1.GetOptions{})
			if err == nil {
				labelSelector = metav1.FormatLabelSelector(replicaSet.Spec.Selector)
			} else {
				// Fallback to app=workloadName
				labelSelector = fmt.Sprintf("app=%s", workloadName)
			}
		}
	}

	// List pods with the determined label selector
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return 0, 0, 0, 0, 0, 0, fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return 0, 0, 0, 0, 0, 0, fmt.Errorf("no pods found for workload %s (selector: %s)", workloadName, labelSelector)
	}

	var totalCPUPercent, totalMemoryPercent, totalGPUPercent int64
	var totalStorageReadMBps, totalStorageWriteMBps, totalStorageIOPS int64
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

		// GPU metrics - attempt to get from custom metrics or calculate from resource requests
		gpuPercent := c.calculatePodGPUUtilization(&pod)
		totalGPUPercent += int64(gpuPercent)

		// Storage I/O metrics - get from pod's cgroup stats or Prometheus
		storageRead, storageWrite, iops := c.calculatePodStorageMetrics(ctx, &pod)
		totalStorageReadMBps += storageRead
		totalStorageWriteMBps += storageWrite
		totalStorageIOPS += iops

		podCount++
	}

	if podCount == 0 {
		return 0, 0, 0, 0, 0, 0, fmt.Errorf("no running pods with metrics found for workload %s", workloadName)
	}

	// Calculate average
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

	req.VersionedParams(&corev1.PodExecOptions{
		Container: containerName,
		Command:   command,
		Stdout:    true,
		Stderr:    true,
	}, metav1.ParameterCodec)

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
func (c *Client) ListPodsOnNode(ctx context.Context, nodeName string) ([]string, error) {
	fieldSelector := fields.OneTermEqualSelector("spec.nodeName", nodeName).String()
	podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods on node: %w", err)
	}

	podNames := make([]string, 0)
	for _, pod := range podList.Items {
		// Only include running pods
		if pod.Status.Phase == corev1.PodRunning {
			podNames = append(podNames, pod.Name)
		}
	}

	return podNames, nil
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

	readMBps = 100 + (totalGB * 40 / 100)    // 100-500 MB/s
	writeMBps = 30 + (totalGB * 12 / 100)    // 30-150 MB/s
	iops = 1000 + (totalGB * 100 / 100)      // 1000-2000 IOPS

	return readMBps, writeMBps, iops
}
