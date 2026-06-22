package manta

import (
	"context"
	"log"
	"os"
	"testing"
)

// TestMantaStubLogs_PrepareDataset calls PrepareDataset and prints [Result] log.
func TestMantaStubLogs_PrepareDataset(t *testing.T) {
	log.SetOutput(os.Stdout)
	c, _ := NewMantaClient(DefaultConfig())
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()

	ctx := context.Background()
	req := &PrepareDatasetRequest{
		WorkloadID:    "wl-001",
		Namespace:     "default",
		PodName:       "train-pod-1",
		DatasetName:   "my-dataset-pvc",
		NodeName:      "worker-1",
		JobType:       "caching",
		AccessPattern: "sequential_read",
		DataPaths:     []string{"/data/a", "/data/b"},
	}
	_, _ = c.PrepareDataset(ctx, req)
}

// TestMantaStubLogs_ReleaseDatasetHint calls ReleaseDatasetHint and prints [Result] log.
func TestMantaStubLogs_ReleaseDatasetHint(t *testing.T) {
	log.SetOutput(os.Stdout)
	c, _ := NewMantaClient(DefaultConfig())
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()

	ctx := context.Background()
	req := &ReleaseDatasetRequest{
		WorkloadID:  "wl-002",
		Namespace:   "default",
		DatasetName: "provisioned-pvc",
		Reason:      "job_completed",
	}
	_ = c.ReleaseDatasetHint(ctx, req)
}

// TestMantaStubLogs_ReportPodPlacement calls ReportPodPlacement and prints [Result] log.
func TestMantaStubLogs_ReportPodPlacement(t *testing.T) {
	log.SetOutput(os.Stdout)
	c, _ := NewMantaClient(DefaultConfig())
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()

	ctx := context.Background()
	req := &PodPlacementReportRequest{
		PodName:   "migrated-pod-1",
		Namespace: "default",
		NodeName:  "worker-2",
	}
	_, _ = c.ReportPodPlacement(ctx, req)
}

// TestMantaStubLogs_ReportDatasetUsage calls ReportDatasetUsage and prints [Result] log.
func TestMantaStubLogs_ReportDatasetUsage(t *testing.T) {
	log.SetOutput(os.Stdout)
	c, _ := NewMantaClient(DefaultConfig())
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()

	ctx := context.Background()
	req := &DatasetUsageReportRequest{
		WorkloadID:    "wl-003",
		DatasetName:   "cache-pvc",
		AccessPattern: "random_read",
		DataPaths:     []string{"/cache/1"},
	}
	_, _ = c.ReportDatasetUsage(ctx, req)
}
