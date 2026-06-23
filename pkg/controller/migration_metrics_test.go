package controller

import (
	"fmt"
	"testing"

	"ai-storage-orchestrator/pkg/types"
)

// TestApplyOptimizedMetrics_UnavailableLeavesUnmeasured asserts that when the optimized
// pod's metrics cannot be measured, the orchestrator does NOT fabricate a savings figure.
// The previous code synthesized OptimizedResources as 50% CPU / 60% memory of the original,
// which misreported the result whenever metrics-server was unavailable.
func TestApplyOptimizedMetrics_UnavailableLeavesUnmeasured(t *testing.T) {
	mc := &MigrationController{}
	job := &MigrationJob{
		ID: "job-1",
		Details: &types.MigrationDetails{
			OriginalResources: &types.ResourceUsage{CPUUsage: 2.0, MemoryUsage: 1024},
		},
	}

	mc.applyOptimizedMetrics(job, nil, fmt.Errorf("metrics-server unavailable"))

	if job.Details.OptimizedResources != nil {
		t.Fatalf("expected OptimizedResources to stay nil (unmeasured) when metrics unavailable, got %+v", job.Details.OptimizedResources)
	}
}

// TestApplyOptimizedMetrics_RecordsMeasured asserts the happy path still records real
// measured metrics.
func TestApplyOptimizedMetrics_RecordsMeasured(t *testing.T) {
	mc := &MigrationController{}
	job := &MigrationJob{ID: "job-2", Details: &types.MigrationDetails{}}
	measured := &types.ResourceUsage{CPUUsage: 1.0, MemoryUsage: 600}

	mc.applyOptimizedMetrics(job, measured, nil)

	if job.Details.OptimizedResources != measured {
		t.Fatalf("expected measured metrics to be recorded, got %+v", job.Details.OptimizedResources)
	}
}
