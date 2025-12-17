package controller

import "context"

// K8sClientInterface defines the interface for k8s operations needed by controllers
type K8sClientInterface interface {
	GetWorkloadReplicas(ctx context.Context, namespace, name, workloadType string) (int32, error)
	GetWorkloadPodMetrics(ctx context.Context, namespace, workloadName string) (cpuPercent, memoryPercent, gpuPercent int32, err error)
	ScaleWorkload(ctx context.Context, namespace, name, workloadType string, replicas int32) error
}
