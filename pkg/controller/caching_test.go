package controller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ai-storage-orchestrator/pkg/gluesys"
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

// recordingGluesys is a thread-safe gluesys.Integration fake that records which
// best-effort hints the caching controller dispatched, so tests can assert that
// eviction/tier-migration notify the storage backend (the real data-plane owner)
// rather than silently faking the work.
type recordingGluesys struct {
	mu             sync.Mutex
	prepareCalls   int
	releaseCalls   int
	placementCalls int
	usageCalls     int
	lastUsageNote  string
}

func (r *recordingGluesys) PrepareDataset(_ context.Context, _ gluesys.DatasetContext) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prepareCalls++
	return nil
}

func (r *recordingGluesys) ReleaseDatasetHint(_ context.Context, _ gluesys.DatasetContext) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releaseCalls++
	return nil
}

func (r *recordingGluesys) ReportPodPlacement(_ context.Context, _ gluesys.DatasetContext) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.placementCalls++
	return nil
}

func (r *recordingGluesys) ReportDatasetUsage(_ context.Context, _ gluesys.DatasetContext, usage gluesys.DatasetUsage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usageCalls++
	r.lastUsageNote = usage.Note
	return nil
}

func (r *recordingGluesys) snapshot() (release, usage int, note string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.releaseCalls, r.usageCalls, r.lastUsageNote
}

// newTestCacheJob builds a minimal active CacheJob for controller unit tests.
// accountedBytes seeds Details.CacheSizeBytes to represent an accounted (but not
// necessarily locally-resident) cache size, used to prove eviction does not credit
// EvictedDataBytes with un-freed accounting.
func newTestCacheJob(id, pvc string, tier types.StorageTier, accountedBytes int64) *CacheJob {
	ctx, cancel := context.WithCancel(context.Background())
	return &CacheJob{
		ID:      id,
		Request: &types.CachingRequest{SourcePVC: pvc, SourceNamespace: "ns", TargetTier: tier},
		Status:  types.CachingStatusActive,
		ctx:     ctx,
		cancel:  cancel,
		Details: &types.CacheDetails{
			SourcePVC:      pvc,
			TargetTier:     tier,
			CacheSizeBytes: accountedBytes,
			Stats:          &types.CacheStats{},
		},
	}
}

// TestPerformEviction_RemovesLocalCacheAndReportsFreedBytes proves eviction does
// real, backend-independent work: it deletes the orchestrator-local warmup cache
// directory, credits EvictedDataBytes with the bytes it ACTUALLY freed, resets
// state to inactive, and dispatches the backend ReleaseDatasetHint.
func TestPerformEviction_RemovesLocalCacheAndReportsFreedBytes(t *testing.T) {
	rec := &recordingGluesys{}
	cc := NewCachingController(nil, rec)
	cc.cacheRoot = t.TempDir()

	job := newTestCacheJob("cache-evict", "ds1", types.TierSSD, 0)

	dir := cc.cacheDir(job.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("0123456789") // 10 bytes
	if err := os.WriteFile(filepath.Join(dir, "f"), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	cc.performEviction(job)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("expected local cache dir removed, stat err=%v", err)
	}
	if job.Details.Stats.EvictedDataBytes != int64(len(payload)) {
		t.Errorf("EvictedDataBytes = %d, want %d (only actually-freed bytes)", job.Details.Stats.EvictedDataBytes, len(payload))
	}
	if job.Details.CacheSizeBytes != 0 {
		t.Errorf("CacheSizeBytes = %d, want 0", job.Details.CacheSizeBytes)
	}
	if job.Status != types.CachingStatusInactive {
		t.Errorf("status = %s, want inactive", job.Status)
	}
	if job.Details.Stats.LastEvictionTime == nil {
		t.Error("LastEvictionTime not set")
	}
	if release, _, _ := rec.snapshot(); release != 1 {
		t.Errorf("ReleaseDatasetHint calls = %d, want 1", release)
	}
}

// TestPerformEviction_NoLocalData_NoFabrication proves that when there is no local
// warmup cache to reclaim, eviction reports 0 freed bytes (never fabricates the
// accounted CacheSizeBytes as "evicted"), still transitions to inactive, and still
// signals the backend.
func TestPerformEviction_NoLocalData_NoFabrication(t *testing.T) {
	rec := &recordingGluesys{}
	cc := NewCachingController(nil, rec)
	cc.cacheRoot = t.TempDir() // empty: no directory exists for this job

	// 12345 accounted bytes that are NOT locally resident — eviction must not
	// claim to have freed them.
	job := newTestCacheJob("cache-nodata", "ds2", types.TierSSD, 12345)

	cc.performEviction(job)

	if job.Details.Stats.EvictedDataBytes != 0 {
		t.Errorf("EvictedDataBytes = %d, want 0 (no local data → no fabricated eviction)", job.Details.Stats.EvictedDataBytes)
	}
	if job.Status != types.CachingStatusInactive {
		t.Errorf("status = %s, want inactive", job.Status)
	}
	if release, _, _ := rec.snapshot(); release != 1 {
		t.Errorf("ReleaseDatasetHint calls = %d, want 1", release)
	}
}

// TestPerformTierMigration_DoesNotFabricateMove proves tier migration records the
// tier-preference change (UpdatedAt advances), notifies the storage backend of the
// new tier via ReportDatasetUsage (whose note names both tiers and states the data
// movement is the backend's responsibility), and NEVER fabricates moved-byte
// counters — the orchestrator does not move cross-tier data itself.
func TestPerformTierMigration_DoesNotFabricateMove(t *testing.T) {
	rec := &recordingGluesys{}
	cc := NewCachingController(nil, rec)

	job := newTestCacheJob("cache-tier", "ds3", types.TierSSD, 5000)
	job.Details.Stats.LoadedDataBytes = 777
	job.Details.Stats.EvictedDataBytes = 0

	cc.performTierMigration(job, types.TierSSD, types.TierNVMe)

	if job.Details.CacheSizeBytes != 5000 {
		t.Errorf("CacheSizeBytes changed to %d (must not fabricate moved bytes)", job.Details.CacheSizeBytes)
	}
	if job.Details.Stats.LoadedDataBytes != 777 || job.Details.Stats.EvictedDataBytes != 0 {
		t.Errorf("byte stats mutated: loaded=%d evicted=%d (no bytes were moved by us)", job.Details.Stats.LoadedDataBytes, job.Details.Stats.EvictedDataBytes)
	}
	if job.Details.UpdatedAt == nil {
		t.Error("UpdatedAt not advanced")
	}
	_, usage, note := rec.snapshot()
	if usage != 1 {
		t.Errorf("ReportDatasetUsage calls = %d, want 1 (backend notified of tier change)", usage)
	}
	if !strings.Contains(note, "ssd") || !strings.Contains(note, "nvme") {
		t.Errorf("tier-change note missing tier names: %q", note)
	}
}
