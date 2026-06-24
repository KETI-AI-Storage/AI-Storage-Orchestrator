package controller

import (
	"testing"

	"ai-storage-orchestrator/pkg/types"
)

// TestUpdateCacheStats_DoesNotFabricate locks in the caching-metrics honesty fix.
// There is no data-plane telemetry source for cache statistics (the
// gluesys.Integration interface has no stats read-back), so updateCacheStats must
// leave hit/throughput/IOPS/latency UNMEASURED rather than synthesize
// realistic-looking numbers from wall-clock time (the previous behavior fabricated
// them via time.Now().Unix()%N). This mirrors the migration savings honesty rule:
// never report fabricated metrics.
func TestUpdateCacheStats_DoesNotFabricate(t *testing.T) {
	cc := NewCachingController(nil, nil)
	job := &CacheJob{
		ID:      "cache-test",
		Request: &types.CachingRequest{TargetTier: types.TierNVMe},
		Details: &types.CacheDetails{
			Stats: &types.CacheStats{},
		},
	}

	cc.updateCacheStats(job)

	s := job.Details.Stats
	if s.TotalRequests != 0 || s.CacheHits != 0 || s.CacheMisses != 0 || s.HitRatio != 0 {
		t.Errorf("hit stats fabricated: %+v (no telemetry source → must stay unmeasured)", s)
	}
	if s.ReadThroughputMBps != 0 || s.WriteThroughputMBps != 0 || s.IOPS != 0 ||
		s.AvgReadLatencyUs != 0 || s.AvgWriteLatencyUs != 0 {
		t.Errorf("I/O stats fabricated: %+v (no telemetry source → must stay unmeasured)", s)
	}
}
