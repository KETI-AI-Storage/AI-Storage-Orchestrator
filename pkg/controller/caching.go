package controller

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-storage-orchestrator/pkg/gluesys"
	"ai-storage-orchestrator/pkg/types"

	"github.com/google/uuid"
)

// defaultCacheRoot is the orchestrator-local directory under which warmup copies
// cached data. It is the orchestrator pod's own (ephemeral) scratch space — NOT
// the tiered cache substrate, which lives in CSI-backed PVCs managed by the
// storage backend. Kept in one place so warmup (writer) and eviction (reaper)
// agree, and so tests can redirect it to a temp dir.
const defaultCacheRoot = "/tmp/ai-storage-cache"

// CachingController manages global caching for AI workloads
// 글로벌 캐싱 컨트롤러: Manta 스토리지 티어 간 데이터 캐싱 관리
type CachingController struct {
	k8sClient     K8sClientInterface
	gluesysClient gluesys.Integration
	caches        map[string]*CacheJob
	cachesMux     sync.RWMutex
	metrics       *types.CachingMetrics
	// cacheRoot is the orchestrator-local warmup cache root (see defaultCacheRoot).
	cacheRoot string
}

// cacheDir returns the orchestrator-local warmup directory for a cache job.
func (cc *CachingController) cacheDir(jobID string) string {
	return filepath.Join(cc.cacheRoot, jobID)
}

// CacheJob represents an active cache
type CacheJob struct {
	ID        string
	Request   *types.CachingRequest
	Status    types.CachingStatus
	Details   *types.CacheDetails
	CreatedAt time.Time
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewCachingController creates a new caching controller.
// gluesysClient can be nil; in that case, all Gluesys-related operations become no-ops.
func NewCachingController(k8sClient K8sClientInterface, gluesysClient gluesys.Integration) *CachingController {
	return &CachingController{
		k8sClient:     k8sClient,
		gluesysClient: gluesysClient,
		caches:        make(map[string]*CacheJob),
		cacheRoot:     defaultCacheRoot,
		metrics: &types.CachingMetrics{
			TotalCaches:  0,
			ActiveCaches: 0,
		},
	}
}

// CreateCache creates a new cache for the specified data
func (cc *CachingController) CreateCache(req *types.CachingRequest) (*types.CachingResponse, error) {
	// Validate request
	if err := cc.validateRequest(req); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Generate unique cache ID
	cacheID := fmt.Sprintf("cache-%s", uuid.New().String()[:8])

	// Create context
	ctx, cancel := context.WithCancel(context.Background())

	// Set defaults
	cachePolicy := req.CachePolicy
	if cachePolicy == "" {
		cachePolicy = types.PolicyLRU
	}

	ttlSeconds := req.TTLSeconds
	if ttlSeconds == 0 {
		ttlSeconds = 3600 // 1 hour default
	}

	sourcePath := req.SourcePath
	if sourcePath == "" {
		sourcePath = "/"
	}

	job := &CacheJob{
		ID:        cacheID,
		Request:   req,
		Status:    types.CachingStatusPending,
		CreatedAt: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
		Details: &types.CacheDetails{
			CreatedAt:       time.Now(),
			SourcePVC:       req.SourcePVC,
			SourceNamespace: req.SourceNamespace,
			SourcePath:      sourcePath,
			TargetTier:      req.TargetTier,
			CachePolicy:     cachePolicy,
			TTLSeconds:      ttlSeconds,
			Priority:        req.Priority,
			Stats: &types.CacheStats{
				TotalRequests: 0,
				CacheHits:     0,
				CacheMisses:   0,
				HitRatio:      0.0,
			},
		},
	}

	// Store cache job
	cc.cachesMux.Lock()
	cc.caches[cacheID] = job
	cc.metrics.TotalCaches++
	cc.metrics.ActiveCaches++
	cc.updateTierCount(req.TargetTier, 1)
	cc.cachesMux.Unlock()

	// Best-effort: 워크로드/데이터셋 정보만 MantaFS에 전달 (캐시 제어는 MantaFS가 담당)
	log.Printf("[KETI->Manta] Sending PrepareDataset for cache job %s dataset=%s", job.ID, job.Request.SourcePVC)
	cc.notifyMantaPrepareDataset(job)
	log.Printf("[KETI->Manta] Sending ReportDatasetUsage for cache job %s dataset=%s", job.ID, job.Request.SourcePVC)
	cc.notifyMantaReportDatasetUsage(job)

	// Start cache loading in background
	go cc.runCacheJob(job)

	log.Printf("Cache %s created for %s/%s (tier: %s, policy: %s)",
		cacheID, req.SourceNamespace, req.SourcePVC, req.TargetTier, cachePolicy)

	return &types.CachingResponse{
		CacheID: cacheID,
		Status:  types.CachingStatusPending,
		Message: "Cache creation initiated",
		Details: job.Details,
	}, nil
}

// GetCache returns the status of a cache
func (cc *CachingController) GetCache(cacheID string) (*types.CachingResponse, error) {
	cc.cachesMux.RLock()
	job, exists := cc.caches[cacheID]
	cc.cachesMux.RUnlock()

	if !exists {
		return nil, fmt.Errorf("cache %s not found", cacheID)
	}

	return &types.CachingResponse{
		CacheID: job.ID,
		Status:  job.Status,
		Message: cc.getStatusMessage(job.Status),
		Details: job.Details,
	}, nil
}

// DeleteCache deletes a cache
func (cc *CachingController) DeleteCache(cacheID string) error {
	cc.cachesMux.Lock()
	job, exists := cc.caches[cacheID]
	if !exists {
		cc.cachesMux.Unlock()
		return fmt.Errorf("cache %s not found", cacheID)
	}

	// Cancel the context to stop any ongoing operations
	if job.cancel != nil {
		job.cancel()
	}

	job.Status = types.CachingStatusInactive
	cc.metrics.ActiveCaches--
	cc.updateTierCount(job.Request.TargetTier, -1)

	delete(cc.caches, cacheID)
	cc.cachesMux.Unlock()

	log.Printf("Cache %s deleted", cacheID)
	return nil
}

// EvictCache evicts cache data (soft delete - keeps metadata)
func (cc *CachingController) EvictCache(cacheID string) error {
	cc.cachesMux.Lock()
	job, exists := cc.caches[cacheID]
	if !exists {
		cc.cachesMux.Unlock()
		return fmt.Errorf("cache %s not found", cacheID)
	}

	job.Status = types.CachingStatusEvicting
	cc.cachesMux.Unlock()

	// Perform eviction in background
	go cc.performEviction(job)

	log.Printf("Cache %s eviction started", cacheID)
	return nil
}

// MigrateTier migrates cache data to a different storage tier
func (cc *CachingController) MigrateTier(req *types.TierMigrationRequest) error {
	cc.cachesMux.Lock()
	job, exists := cc.caches[req.CacheID]
	if !exists {
		cc.cachesMux.Unlock()
		return fmt.Errorf("cache %s not found", req.CacheID)
	}

	oldTier := job.Request.TargetTier

	// Update tier counts
	cc.updateTierCount(oldTier, -1)
	cc.updateTierCount(req.TargetTier, 1)

	job.Request.TargetTier = req.TargetTier
	job.Details.TargetTier = req.TargetTier
	now := time.Now()
	job.Details.UpdatedAt = &now
	cc.cachesMux.Unlock()

	// Perform tier migration in background
	go cc.performTierMigration(job, oldTier, req.TargetTier)

	log.Printf("Cache %s tier migration started: %s -> %s",
		req.CacheID, oldTier, req.TargetTier)
	return nil
}

// WarmupCache pre-loads data into cache (paths에 명시된 경로만 cp -a로 적재; glob pattern 미지원).
func (cc *CachingController) WarmupCache(req *types.CacheWarmupRequest) error {
	cc.cachesMux.RLock()
	job, exists := cc.caches[req.CacheID]
	cc.cachesMux.RUnlock()

	if !exists {
		return fmt.Errorf("cache %s not found", req.CacheID)
	}

	if len(req.Paths) == 0 {
		return fmt.Errorf("warmup: paths must contain at least one explicit file or directory path")
	}

	if req.Pattern != "" {
		return fmt.Errorf("warmup: pattern-based prefetch is not supported; pass explicit file/directory paths in paths[] only")
	}

	if req.Async {
		go cc.performWarmup(job, req.Paths)
	} else {
		if err := cc.performWarmup(job, req.Paths); err != nil {
			return err
		}
	}

	log.Printf("Cache %s warmup started (async: %v)", req.CacheID, req.Async)
	return nil
}

// ApplyPolicyDecision applies a decision from the policy engine
// 정책 엔진으로부터 받은 결정을 적용
func (cc *CachingController) ApplyPolicyDecision(decision *types.CachePolicyDecision) error {
	log.Printf("Applying policy decision: action=%s, probability=%.2f, horizon=%s",
		decision.Action, decision.Probability, decision.Horizon)

	switch decision.Action {
	case types.ActionCreateCache:
		// Policy engine decided to create a cache
		// This would be called with additional context from the policy engine
		log.Printf("Policy decision: Create cache (reason: %s)", decision.Reason)
		return nil

	case types.ActionEvictCache:
		if decision.TargetCacheID == "" {
			return fmt.Errorf("target_cache_id required for evict action")
		}
		return cc.EvictCache(decision.TargetCacheID)

	case types.ActionMigrateTier:
		if decision.TargetCacheID == "" || decision.TargetTier == "" {
			return fmt.Errorf("target_cache_id and target_tier required for migrate action")
		}
		return cc.MigrateTier(&types.TierMigrationRequest{
			CacheID:    decision.TargetCacheID,
			TargetTier: decision.TargetTier,
			Reason:     decision.Reason,
		})

	case types.ActionWarmupCache:
		if decision.TargetCacheID == "" {
			return fmt.Errorf("target_cache_id required for warmup action")
		}
		return cc.WarmupCache(&types.CacheWarmupRequest{
			CacheID: decision.TargetCacheID,
			Async:   true,
		})

	case types.ActionNoAction:
		log.Printf("Policy decision: No action needed")
		return nil

	default:
		return fmt.Errorf("unknown action: %s", decision.Action)
	}
}

// ListCaches returns all caches
func (cc *CachingController) ListCaches() []*types.CachingResponse {
	cc.cachesMux.RLock()
	defer cc.cachesMux.RUnlock()

	result := make([]*types.CachingResponse, 0, len(cc.caches))
	for _, job := range cc.caches {
		result = append(result, &types.CachingResponse{
			CacheID: job.ID,
			Status:  job.Status,
			Message: cc.getStatusMessage(job.Status),
			Details: job.Details,
		})
	}
	return result
}

// GetMetrics returns overall caching metrics
func (cc *CachingController) GetMetrics() *types.CachingMetrics {
	cc.cachesMux.RLock()
	defer cc.cachesMux.RUnlock()

	// Calculate global hit ratio
	totalHits := int64(0)
	totalRequests := int64(0)
	totalCachedBytes := int64(0)
	totalReadThroughput := int64(0)
	totalWriteThroughput := int64(0)
	totalIOPS := int64(0)

	for _, job := range cc.caches {
		if job.Details.Stats != nil {
			totalHits += job.Details.Stats.CacheHits
			totalRequests += job.Details.Stats.TotalRequests
			totalCachedBytes += job.Details.Stats.CachedDataBytes
			totalReadThroughput += job.Details.Stats.ReadThroughputMBps
			totalWriteThroughput += job.Details.Stats.WriteThroughputMBps
			totalIOPS += job.Details.Stats.IOPS
		}
	}

	globalHitRatio := float64(0)
	if totalRequests > 0 {
		globalHitRatio = float64(totalHits) / float64(totalRequests)
	}

	metrics := *cc.metrics
	metrics.GlobalHitRatio = globalHitRatio
	metrics.TotalCachedBytes = totalCachedBytes
	metrics.TotalReadThroughputMBps = totalReadThroughput
	metrics.TotalWriteThroughputMBps = totalWriteThroughput
	metrics.TotalIOPS = totalIOPS

	// Estimate savings (simplified calculation)
	// Assumes cache hit saves 10ms average and 1MB average read
	metrics.EstimatedIOSavedBytes = totalHits * 1024 * 1024 // 1MB per hit
	metrics.EstimatedTimeSavedMs = totalHits * 10           // 10ms per hit

	return &metrics
}

// runCacheJob runs the cache loading job
func (cc *CachingController) runCacheJob(job *CacheJob) {
	cc.cachesMux.Lock()
	job.Status = types.CachingStatusLoading
	cc.cachesMux.Unlock()

	log.Printf("Cache %s: Loading data from %s/%s to %s tier (prefetch=%v)",
		job.ID, job.Request.SourceNamespace, job.Request.SourcePVC, job.Request.TargetTier, job.Request.Prefetch)

	root := filepath.Clean(job.Request.SourcePath)
	if root == "" || root == "." {
		root = "/"
	}

	if job.Request.Prefetch {
		t0 := time.Now()
		if cc.k8sClient == nil {
			cc.markJobFailed(job, "prefetch: kubernetes client is not configured")
			return
		}

		log.Printf("cache %s: creating cache-prefetch pod namespace=%s pvc=%s sourcePath=%s",
			job.ID, job.Request.SourceNamespace, job.Request.SourcePVC, root)

		bytesLoaded, fileCount, err := cc.k8sClient.RunCachePrefetchPod(
			job.ctx,
			job.Request.SourceNamespace,
			job.Request.SourcePVC,
			root,
			job.ID,
		)
		if err != nil {
			cc.markJobFailed(job, fmt.Sprintf("prefetch pod failed: %v", err))
			return
		}
		dur := time.Since(t0)
		cc.cachesMux.Lock()
		job.Status = types.CachingStatusActive
		job.Details.SourceSizeBytes = bytesLoaded
		job.Details.CacheSizeBytes = bytesLoaded
		if job.Details.Stats != nil {
			job.Details.Stats.LoadedDataBytes += bytesLoaded
		}
		now := time.Now()
		job.Details.UpdatedAt = &now
		cc.cachesMux.Unlock()
		log.Printf("cache %s: prefetch pod complete bytes=%d files=%d duration=%s", job.ID, bytesLoaded, fileCount, dur.String())
		cc.collectStats(job)
		return
	}

	select {
	case <-job.ctx.Done():
		log.Printf("Cache %s: Loading cancelled", job.ID)
		return
	default:
	}

	allFiles, err := listDataFilesSorted(root)
	var sumSrc int64
	if err == nil {
		for _, f := range allFiles {
			if st, e := os.Stat(f); e == nil {
				sumSrc += st.Size()
			}
		}
	} else {
		log.Printf("cache %s: source path walk %q: %v (size left 0)", job.ID, root, err)
	}

	cc.cachesMux.Lock()
	job.Status = types.CachingStatusActive
	job.Details.SourceSizeBytes = sumSrc
	job.Details.CacheSizeBytes = sumSrc
	now := time.Now()
	job.Details.UpdatedAt = &now
	cc.cachesMux.Unlock()
	log.Printf("Cache %s: Active (prefetch disabled, source bytes=%d)", job.ID, sumSrc)
	cc.collectStats(job)
}

// collectStats periodically collects cache statistics
func (cc *CachingController) collectStats(job *CacheJob) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-job.ctx.Done():
			return
		case <-ticker.C:
			cc.updateCacheStats(job)
		}
	}
}

// updateCacheStats refreshes cache statistics from the data-plane telemetry
// source. This is the polling integration point driven by collectStats (30s).
//
// HONESTY: there is currently NO telemetry source. The Gluesys/Manta
// gluesys.Integration interface exposes only hint/report methods (PrepareDataset,
// ReportDatasetUsage, …) and no stats read-back, so hit ratio, throughput, IOPS
// and latency are left UNMEASURED rather than fabricated. Previously these were
// synthesized from wall-clock time (time.Now().Unix()%N), producing
// realistic-looking but entirely invented numbers — the same anti-pattern the
// migration savings path was already cleaned of (see CLAUDE.md: never report
// fabricated metrics; report unmeasured instead).
//
// When a real data-plane stats API is added, read it here (under cachesMux) and
// populate job.Details.Stats from the measured values.
func (cc *CachingController) updateCacheStats(_ *CacheJob) {
	// No telemetry source available — intentionally do not fabricate statistics.
}

// performEviction evicts a cache. It does the one piece of data-plane work the
// orchestrator legitimately owns — reclaiming the orchestrator-local warmup copy
// (see defaultCacheRoot) — and delegates eviction of the tiered (CSI-backed) cache
// to the storage backend via ReleaseDatasetHint.
//
// HONESTY: EvictedDataBytes is credited only with bytes we ACTUALLY freed locally,
// never with the accounted CacheSizeBytes (which may not be locally resident). If
// there is no local copy, freed is 0 — we do not fabricate an eviction figure. The
// real tiered-store reclamation is the backend's responsibility and is not measured
// here. (Previously this slept 1s and claimed it evicted CacheSizeBytes without
// freeing anything — the same fabrication anti-pattern as the cache-stats fix.)
func (cc *CachingController) performEviction(job *CacheJob) {
	log.Printf("Cache %s: Starting eviction", job.ID)

	select {
	case <-job.ctx.Done():
		return
	default:
	}

	// Reclaim the orchestrator-local warmup directory (the only data this
	// controller wrote locally). Credit only what we truly freed.
	dir := cc.cacheDir(job.ID)
	var freed int64
	if _, err := os.Stat(dir); err == nil {
		freed = dirSize(dir)
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			cc.markJobFailed(job, fmt.Sprintf("eviction: remove local cache %q: %v", dir, rmErr))
			return
		}
	}

	cc.cachesMux.Lock()
	if job.Details.Stats != nil {
		job.Details.Stats.EvictedDataBytes += freed
	}
	job.Details.CacheSizeBytes = 0
	job.Status = types.CachingStatusInactive
	now := time.Now()
	job.Details.UpdatedAt = &now
	if job.Details.Stats != nil {
		job.Details.Stats.LastEvictionTime = &now
	}
	cc.cachesMux.Unlock()

	// Best-effort: ask the storage backend to evict the tiered copy.
	cc.sendReleaseHint(job)

	log.Printf("Cache %s: Eviction complete (freed %d bytes of orchestrator-local warmup cache; tiered-store eviction delegated to backend)", job.ID, freed)
}

// performTierMigration records a cache's tier-preference change and notifies the
// storage backend. The orchestrator does NOT relocate data across physical tiers
// itself — with a CSI-backed tiered store that is the storage backend's job; the
// local tier accounting (tier counts + Details.TargetTier) was already applied
// synchronously in MigrateTier.
//
// HONESTY: no *Bytes counter is touched here — no bytes were moved by us, so
// reporting any would be fabrication. The cross-tier data movement is delegated to
// the backend (and is not measured here). (Previously this slept 3s and logged
// "Tier migration completed" as if a move had happened.)
func (cc *CachingController) performTierMigration(job *CacheJob, oldTier, newTier types.StorageTier) {
	log.Printf("Cache %s: recording tier preference %s -> %s (data movement delegated to storage backend)", job.ID, oldTier, newTier)

	select {
	case <-job.ctx.Done():
		return
	default:
	}

	cc.cachesMux.Lock()
	now := time.Now()
	job.Details.UpdatedAt = &now
	cc.cachesMux.Unlock()

	// Best-effort: notify the backend of the new tier preference.
	cc.sendTierChangeHint(job, oldTier, newTier)

	log.Printf("Cache %s: tier preference recorded (%s -> %s); cross-tier data movement delegated to storage backend (not measured here)", job.ID, oldTier, newTier)
}

// performWarmup pre-loads data into cache (paths는 실제 경로만; glob pattern 미사용).
func (cc *CachingController) performWarmup(job *CacheJob, paths []string) error {
	log.Printf("Cache %s: Starting warmup (paths: %v)", job.ID, paths)

	targetRoot := cc.cacheDir(job.ID)
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return fmt.Errorf("warmup mkdir %q: %w", targetRoot, err)
	}

	t0 := time.Now()
	sort.Strings(paths)
	totalLoaded := int64(0)
	for _, p := range paths {
		select {
		case <-job.ctx.Done():
			return job.ctx.Err()
		default:
		}
		loaded, err := cc.prefetchPath(job.ctx, p, targetRoot)
		if err != nil {
			return fmt.Errorf("warmup prefetch path %q: %w", p, err)
		}
		totalLoaded += loaded
	}

	cc.cachesMux.Lock()
	if job.Details.Stats != nil {
		job.Details.Stats.LoadedDataBytes += totalLoaded
	}
	now := time.Now()
	job.Details.UpdatedAt = &now
	cc.cachesMux.Unlock()

	log.Printf("Cache %s: Warmup completed bytes=%d duration=%s", job.ID, totalLoaded, time.Since(t0).String())
	return nil
}

func (cc *CachingController) prefetchPath(ctx context.Context, srcPath, targetRoot string) (int64, error) {
	info, err := os.Stat(srcPath)
	if err != nil {
		return 0, fmt.Errorf("stat %q: %w", srcPath, err)
	}
	destPath := filepath.Join(targetRoot, filepath.Base(srcPath))
	cmd := exec.CommandContext(ctx, "cp", "-a", srcPath, destPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("cp -a %q -> %q failed: %w (%s)", srcPath, destPath, err, strings.TrimSpace(string(out)))
	}
	if info.IsDir() {
		return dirSize(destPath), nil
	}
	return info.Size(), nil
}

func (cc *CachingController) markJobFailed(job *CacheJob, msg string) {
	cc.cachesMux.Lock()
	defer cc.cachesMux.Unlock()
	job.Status = types.CachingStatusFailed
	job.Details.ErrorMessage = msg
	now := time.Now()
	job.Details.UpdatedAt = &now
	log.Printf("cache %s: FAILED %s", job.ID, msg)
}

// listDataFilesSorted는 root 아래의 일반 파일만 재귀적으로 나열한다(글로브 패턴 없음).
func listDataFilesSorted(root string) ([]string, error) {
	fi, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		if fi.Mode().IsRegular() {
			return []string{filepath.Clean(root)}, nil
		}
		return nil, fmt.Errorf("%q is not a directory or regular file", root)
	}
	rootClean := filepath.Clean(root)
	var out []string
	err = filepath.WalkDir(rootClean, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// shardIndexAndTotal은 WorkloadSelector 라벨에서 샤드 인덱스·총 샤드 수를 읽는다.
func shardIndexAndTotal(req *types.CachingRequest) (idx, total int) {
	idx, total = 0, 1
	if req == nil || req.WorkloadSelector == nil || req.WorkloadSelector.Labels == nil {
		return
	}
	lbl := req.WorkloadSelector.Labels
	if v, ok := lbl["ai-storage.keti/shard-index"]; ok {
		if n, e := strconv.Atoi(strings.TrimSpace(v)); e == nil && n >= 0 {
			idx = n
		}
	}
	if v, ok := lbl["ai-storage.keti/shard-count"]; ok {
		if n, e := strconv.Atoi(strings.TrimSpace(v)); e == nil && n > 0 {
			total = n
		}
	}
	return
}

// filesAssignedToShard는 정렬된 전체 파일 목록을 샤드 수로 균등 분할한 뒤 해당 샤드에 속한 경로만 반환한다.
func filesAssignedToShard(sorted []string, shardIdx, totalShards int) []string {
	if totalShards < 1 {
		totalShards = 1
	}
	shardIdx = ((shardIdx % totalShards) + totalShards) % totalShards
	var out []string
	for i, p := range sorted {
		if i%totalShards == shardIdx {
			out = append(out, p)
		}
	}
	return out
}

// prefetchFilePaths는 root 기준 상대 경로를 유지하며 destRoot 아래로 실제 복사한다.
func (cc *CachingController) prefetchFilePaths(ctx context.Context, root string, files []string, destRoot string) (int64, error) {
	rootClean := filepath.Clean(root)
	var total int64
	for _, f := range files {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
		fClean := filepath.Clean(f)
		rel, err := filepath.Rel(rootClean, fClean)
		if err != nil || strings.HasPrefix(rel, "..") {
			return total, fmt.Errorf("file %q is not under root %q", f, rootClean)
		}
		dst := filepath.Join(destRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return total, fmt.Errorf("mkdir %q: %w", filepath.Dir(dst), err)
		}
		n, err := copyRegularFile(ctx, fClean, dst)
		if err != nil {
			return total, fmt.Errorf("copy %q -> %q: %w", fClean, dst, err)
		}
		total += n
	}
	return total, nil
}

func copyRegularFile(ctx context.Context, src, dst string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	sf, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer sf.Close()
	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer df.Close()
	n, err := io.Copy(df, sf)
	if err != nil {
		return n, err
	}
	if err := df.Sync(); err != nil {
		return n, err
	}
	return n, nil
}

func dirSize(root string) int64 {
	var size int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}

// validateRequest validates a caching request
func (cc *CachingController) validateRequest(req *types.CachingRequest) error {
	if req.SourcePVC == "" {
		return fmt.Errorf("source_pvc is required")
	}
	if req.SourceNamespace == "" {
		return fmt.Errorf("source_namespace is required")
	}
	if req.TargetTier == "" {
		return fmt.Errorf("target_tier is required")
	}

	// Validate tier
	switch req.TargetTier {
	case types.TierNVMe, types.TierSSD, types.TierHDD, types.TierS3, types.TierAuto:
		// Valid
	default:
		return fmt.Errorf("invalid target_tier: %s (must be nvme, ssd, hdd, s3, or auto)", req.TargetTier)
	}

	// Validate cache policy
	if req.CachePolicy != "" {
		switch req.CachePolicy {
		case types.PolicyLRU, types.PolicyLFU, types.PolicyFIFO, types.PolicyTTL:
			// Valid
		default:
			return fmt.Errorf("invalid cache_policy: %s (must be lru, lfu, fifo, or ttl)", req.CachePolicy)
		}
	}

	return nil
}

// updateTierCount updates the tier count in metrics
func (cc *CachingController) updateTierCount(tier types.StorageTier, delta int64) {
	switch tier {
	case types.TierNVMe:
		cc.metrics.NVMeCacheCount += delta
	case types.TierSSD:
		cc.metrics.SSDCacheCount += delta
	case types.TierHDD:
		cc.metrics.HDDCacheCount += delta
	}
}

// getStatusMessage returns a human-readable status message
func (cc *CachingController) getStatusMessage(status types.CachingStatus) string {
	switch status {
	case types.CachingStatusPending:
		return "Cache creation pending"
	case types.CachingStatusLoading:
		return "Loading data into cache"
	case types.CachingStatusActive:
		return "Cache is active and serving requests"
	case types.CachingStatusEvicting:
		return "Cache is being evicted"
	case types.CachingStatusInactive:
		return "Cache is inactive"
	case types.CachingStatusFailed:
		return "Cache operation failed"
	default:
		return "Unknown status"
	}
}

// buildDatasetContext builds DatasetContext for a cache job.
func (cc *CachingController) buildDatasetContext(job *CacheJob) gluesys.DatasetContext {
	if job == nil || job.Request == nil || job.Details == nil {
		return gluesys.DatasetContext{}
	}
	return gluesys.DatasetContext{
		// 캐싱 워크로드 식별자는 별도 필드가 없으므로 cache ID 를 사용한다.
		WorkloadName: job.ID,
		DatasetName:  job.Request.SourcePVC,
		Namespace:    job.Request.SourceNamespace,
		NodeName:     "", // 캐싱은 노드 비의존적인 글로벌 캐시로 간주
		PVCName:      job.Request.SourcePVC,
		PVName:       "",
	}
}

// buildDatasetUsage builds DatasetUsage for a cache job.
func (cc *CachingController) buildDatasetUsage(job *CacheJob) gluesys.DatasetUsage {
	if job == nil || job.Request == nil {
		return gluesys.DatasetUsage{}
	}
	return gluesys.DatasetUsage{
		TotalBytesRead:    0,
		TotalBytesWritten: 0,
		AccessPattern:     "",
		Note:              "global cache warmup (stub usage)",
	}
}

// notifyGluesysPrepareDataset sends a best-effort PrepareDataset hint to Gluesys.
func (cc *CachingController) notifyMantaPrepareDataset(job *CacheJob) {
	if cc.gluesysClient == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		dctx := cc.buildDatasetContext(job)
		if dctx.DatasetName == "" {
			return
		}
		if err := cc.gluesysClient.PrepareDataset(ctx, dctx); err != nil {
			log.Printf("gluesys PrepareDataset error (cache %s): %v", job.ID, err)
		}
	}()
}

// notifyGluesysReportDatasetUsage sends a best-effort ReportDatasetUsage to Gluesys.
func (cc *CachingController) notifyMantaReportDatasetUsage(job *CacheJob) {
	if cc.gluesysClient == nil || job == nil || job.Request == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		dctx := cc.buildDatasetContext(job)
		if dctx.DatasetName == "" {
			return
		}
		usage := cc.buildDatasetUsage(job)
		if err := cc.gluesysClient.ReportDatasetUsage(ctx, dctx, usage); err != nil {
			log.Printf("gluesys ReportDatasetUsage error (cache %s): %v", job.ID, err)
		}
	}()
}

// sendReleaseHint synchronously sends a best-effort ReleaseDatasetHint to the
// storage backend (the real owner of tiered-cache eviction). It is called from the
// already-background performEviction goroutine, so a synchronous call here keeps
// the eviction result deterministic without blocking any request handler.
func (cc *CachingController) sendReleaseHint(job *CacheJob) {
	if cc.gluesysClient == nil {
		return
	}
	dctx := cc.buildDatasetContext(job)
	if dctx.DatasetName == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := cc.gluesysClient.ReleaseDatasetHint(ctx, dctx); err != nil {
		log.Printf("gluesys ReleaseDatasetHint error (cache %s): %v", job.ID, err)
	}
}

// sendTierChangeHint synchronously notifies the storage backend of a cache's new
// tier preference. The Integration interface has no tier-specific verb, so we reuse
// ReportDatasetUsage with an explicit note: the actual cross-tier data movement is
// the backend's responsibility (not performed/measured by the orchestrator). Called
// from the already-background performTierMigration goroutine.
func (cc *CachingController) sendTierChangeHint(job *CacheJob, oldTier, newTier types.StorageTier) {
	if cc.gluesysClient == nil {
		return
	}
	dctx := cc.buildDatasetContext(job)
	if dctx.DatasetName == "" {
		return
	}
	usage := cc.buildDatasetUsage(job)
	usage.Note = fmt.Sprintf("tier preference changed %s -> %s; cross-tier data movement is the storage backend's responsibility (not performed/measured by orchestrator)", oldTier, newTier)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := cc.gluesysClient.ReportDatasetUsage(ctx, dctx, usage); err != nil {
		log.Printf("gluesys ReportDatasetUsage error (cache %s): %v", job.ID, err)
	}
}
