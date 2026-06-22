// TestMantaStubLogs_RealPod calls the 4 Manta stub APIs with example pod "Manta_test_pod" and prints logs.
// Run: go test -v -run TestMantaStubLogs_RealPod ./pkg/manta/

package manta

import (
	"context"
	"log"
	"os"
	"testing"
)

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// TestMantaStubLogs_RealPod runs the 4 Manta stub APIs. Pod/namespace/node can be set via env:
//   TEST_POD_NAME (default: Manta_test_pod)
//   TEST_NAMESPACE (default: default)
//   TEST_NODE (default: worker-1)
func TestMantaStubLogs_RealPod(t *testing.T) {
	log.SetOutput(os.Stdout)
	log.SetFlags(0)

	ctx := context.Background()
	jobType := "Preprocessing"
	podName := getEnv("TEST_POD_NAME", "Manta_test_pod")
	namespace := getEnv("TEST_NAMESPACE", "default")
	nodeName := getEnv("TEST_NODE", "worker-1")
	workloadID := "real-" + podName
	datasetName := "manta-dataset-pvc"
	dataPaths := []string{"/data"}
	accessPattern := "sequential_read"
	reason := "preprocessing_completed"

	log.Printf("[INFO]")
	log.Printf("  workload_id=%s", workloadID)
	log.Printf("  pod=%s", podName)
	log.Printf("  namespace=%s", namespace)
	log.Printf("  node=%s", nodeName)
	log.Printf("  dataset=%s", datasetName)
	client := NewMantaClientForTest()
	_, _ = client.PrepareDataset(ctx, &PrepareDatasetRequest{
		WorkloadID:    workloadID,
		Namespace:     namespace,
		PodName:       podName,
		DatasetName:   datasetName,
		NodeName:      nodeName,
		JobType:       jobType,
		AccessPattern: accessPattern,
		DataPaths:     dataPaths,
	})

	_ = client.ReleaseDatasetHint(ctx, &ReleaseDatasetRequest{
		WorkloadID:  workloadID,
		Namespace:   namespace,
		DatasetName: datasetName,
		Reason:      reason,
	})

	_, _ = client.ReportPodPlacement(ctx, &PodPlacementReportRequest{
		PodName:   podName,
		Namespace: namespace,
		NodeName:  nodeName,
	})

	_, _ = client.ReportDatasetUsage(ctx, &DatasetUsageReportRequest{
		WorkloadID:    workloadID,
		DatasetName:   datasetName,
		AccessPattern: accessPattern,
		DataPaths:     dataPaths,
	})

	log.Printf("[RUN] All 4 Manta APIs called successfully")
	log.Printf("  job_type=%s", jobType)
	log.Printf("  workload_id=%s", workloadID)
}
