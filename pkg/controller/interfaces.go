package controller

import (
	"context"

	"ai-storage-orchestrator/pkg/types"
)

// K8sClientInterface defines the interface for k8s operations needed by controllers
type K8sClientInterface interface {
	// Autoscaling operations
	GetWorkloadReplicas(ctx context.Context, namespace, name, workloadType string) (int32, error)
	GetWorkloadPodMetrics(ctx context.Context, namespace, workloadName string) (cpuPercent, memoryPercent, gpuPercent int32, storageReadMBps, storageWriteMBps, storageIOPS int64, err error)
	ScaleWorkload(ctx context.Context, namespace, name, workloadType string, replicas int32) error

	// Loadbalancing operations
	ListNodes(ctx context.Context) ([]string, error)
	GetNodeMetrics(ctx context.Context, nodeName string) (cpuPercent, memoryPercent int32, err error)
	GetNodeCapacity(ctx context.Context, nodeName string) (cpuCapacity, memoryCapacity string, gpuCapacity int32, err error)
	GetNodePodCount(ctx context.Context, nodeName string) (int32, error)
	GetNodeLabel(ctx context.Context, nodeName string, labelKey string) (string, error)
	GetNodeGPUUtilization(ctx context.Context, nodeName string) (int32, error)
	ListPodsOnNode(ctx context.Context, nodeName string) ([]types.PodRef, error)

	// Storage I/O operations for AI/ML workloads
	GetNodeStorageMetrics(ctx context.Context, nodeName string) (readMBps, writeMBps, iops int64, utilization int32, err error)

	// Provisioning operations
	CreatePVC(ctx context.Context, namespace, name, size, storageClass, accessMode string, labels map[string]string) error
	CreatePVCWithAnnotations(ctx context.Context, namespace, name, size, storageClass, accessMode string, labels, annotations map[string]string) error
	CreateBindingPodForPVC(ctx context.Context, namespace, pvcName, podName string) error
	DeletePod(ctx context.Context, namespace, name string) error
	DeletePVC(ctx context.Context, namespace, name string) error
	WaitForPVCReady(ctx context.Context, namespace, name string, timeoutSeconds int) error

	// Preemption operations
	GetPodResourceInfo(ctx context.Context, namespace, name string) (*types.PodResourceInfo, error)
	EvictPod(ctx context.Context, namespace, name string, gracePeriodSeconds int64) error

	// Caching operations
	RunCachePrefetchPod(ctx context.Context, namespace, pvcName, sourcePath, jobID string) (bytesLoaded int64, fileCount int64, err error)
}
