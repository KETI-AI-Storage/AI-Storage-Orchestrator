package etri

import (
	"context"
	"testing"

	"ai-storage-orchestrator/pkg/controller"
	"ai-storage-orchestrator/pkg/types"
)

// fakeK8sClient is a minimal stub that satisfies controller.K8sClientInterface
// without making real Kubernetes calls. Only methods that are actually used
// by Service are implemented; the rest panic if called unexpectedly.
type fakeK8sClient struct{}

func (f *fakeK8sClient) GetWorkloadReplicas(ctx context.Context, namespace, name, workloadType string) (int32, error) {
	return 1, nil
}
func (f *fakeK8sClient) GetWorkloadPodMetrics(ctx context.Context, namespace, workloadName string) (int32, int32, int32, int64, int64, int64, error) {
	return 0, 0, 0, 0, 0, 0, nil
}
func (f *fakeK8sClient) ScaleWorkload(ctx context.Context, namespace, name, workloadType string, replicas int32) error {
	return nil
}
func (f *fakeK8sClient) ListNodes(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (f *fakeK8sClient) GetNodeMetrics(ctx context.Context, nodeName string) (int32, int32, error) {
	return 0, 0, nil
}
func (f *fakeK8sClient) GetNodeCapacity(ctx context.Context, nodeName string) (string, string, int32, error) {
	return "", "", 0, nil
}
func (f *fakeK8sClient) GetNodePodCount(ctx context.Context, nodeName string) (int32, error) {
	return 0, nil
}
func (f *fakeK8sClient) GetNodeLabel(ctx context.Context, nodeName string, labelKey string) (string, error) {
	return "", nil
}
func (f *fakeK8sClient) GetNodeGPUUtilization(ctx context.Context, nodeName string) (int32, error) {
	return 0, nil
}
func (f *fakeK8sClient) ListPodsOnNode(ctx context.Context, nodeName string) ([]types.PodRef, error) {
	return nil, nil
}
func (f *fakeK8sClient) GetNodeStorageMetrics(ctx context.Context, nodeName string) (int64, int64, int64, int32, error) {
	return 0, 0, 0, 0, nil
}
func (f *fakeK8sClient) CreatePVCWithAnnotations(ctx context.Context, namespace, name, size, storageClass, accessMode string, labels, annotations map[string]string) error {
	return nil
}
func (f *fakeK8sClient) CreateBindingPodForPVC(ctx context.Context, namespace, pvcName, podName string) error {
	return nil
}
func (f *fakeK8sClient) DeletePod(ctx context.Context, namespace, name string) error {
	return nil
}
func (f *fakeK8sClient) CreatePVC(ctx context.Context, namespace, name, size, storageClass, accessMode string, labels map[string]string) error {
	return nil
}
func (f *fakeK8sClient) DeletePVC(ctx context.Context, namespace, name string) error {
	return nil
}
func (f *fakeK8sClient) WaitForPVCReady(ctx context.Context, namespace, name string, timeoutSeconds int) error {
	return nil
}
func (f *fakeK8sClient) GetPodResourceInfo(ctx context.Context, namespace, name string) (*types.PodResourceInfo, error) {
	return nil, nil
}
func (f *fakeK8sClient) EvictPod(ctx context.Context, namespace, name string, gracePeriodSeconds int64) error {
	return nil
}

// RunCachePrefetchPod 는 K8sClientInterface 의 캐싱 메서드 추가에 맞춘 stub 이다.
func (f *fakeK8sClient) RunCachePrefetchPod(ctx context.Context, namespace, pvcName, sourcePath, jobID string) (bytesLoaded int64, fileCount int64, err error) {
	return 0, 0, nil
}

// Ensure fakeK8sClient implements the interface at compile time.
var _ controller.K8sClientInterface = (*fakeK8sClient)(nil)

func TestService_RegisterAndGetWorkloadStatus_WithMetadataOnly(t *testing.T) {
	ctx := context.Background()
	repo := NewInMemoryRepository()
	svc := NewService(repo, &fakeK8sClient{})

	meta := &EtriWorkloadMetadata{
		WorkloadName: "example-workload",
		Namespace:    "default",
		Stage:        "train",
		Framework:    "kubeflow",
		Datasets: []DatasetReference{
			{
				DatasetName:   "imagenet",
				DatasetID:     "ds-001",
				LogicalPath:   "/datasets/imagenet",
				AccessPattern: "read",
			},
		},
	}

	if err := svc.RegisterWorkloadMetadata(ctx, meta); err != nil {
		t.Fatalf("RegisterWorkloadMetadata returned error: %v", err)
	}

	status, err := svc.GetWorkloadStatus(ctx, WorkloadRef{
		WorkloadName: "example-workload",
		Namespace:    "default",
	})
	if err != nil {
		t.Fatalf("GetWorkloadStatus returned error: %v", err)
	}
	if status == nil {
		t.Fatalf("expected non-nil status")
	}
	if status.WorkloadName != "example-workload" || status.Namespace != "default" {
		t.Fatalf("unexpected workload identity in status: %+v", status)
	}
	if status.DatasetName != "imagenet" {
		t.Fatalf("expected dataset_name 'imagenet', got %q", status.DatasetName)
	}
}

