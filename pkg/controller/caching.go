package controller

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"ai-storage-orchestrator/pkg/types"

	"github.com/google/uuid"
)

// CachingController manages global caching for AI workloads
// 글로벌 캐싱 컨트롤러: Manta 스토리지 티어 간 데이터 캐싱 관리
type CachingController struct {
	k8sClient   K8sClientInterface
	caches      map[string]*CacheJob
	cachesMux   sync.RWMutex
	metrics     *types.CachingMetrics
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

// NewCachingController creates a new caching controller
func NewCachingController(k8sClient K8sClientInterface) *CachingController {
	return &CachingController{
		k8sClient: k8sClient,
		caches:    make(map[string]*CacheJob),
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

// WarmupCache pre-loads data into cache
func (cc *CachingController) WarmupCache(req *types.CacheWarmupRequest) error {
	cc.cachesMux.RLock()
	job, exists := cc.caches[req.CacheID]
	cc.cachesMux.RUnlock()

	if !exists {
		return fmt.Errorf("cache %s not found", req.CacheID)
	}

	if req.Async {
		go cc.performWarmup(job, req.Paths, req.Pattern)
	} else {
		cc.performWarmup(job, req.Paths, req.Pattern)
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
	// Update status to loading
	cc.cachesMux.Lock()
	job.Status = types.CachingStatusLoading
	cc.cachesMux.Unlock()

	log.Printf("Cache %s: Loading data from %s/%s to %s tier",
		job.ID, job.Request.SourceNamespace, job.Request.SourcePVC, job.Request.TargetTier)

	// Simulate cache loading (in real implementation, this would interact with Manta storage)
	select {
	case <-job.ctx.Done():
		log.Printf("Cache %s: Loading cancelled", job.ID)
		return
	case <-time.After(2 * time.Second): // Simulated loading time
	}

	// Get PVC size (simulated)
	sourceSize := int64(10 * 1024 * 1024 * 1024) // 10GB simulated

	// Update details
	cc.cachesMux.Lock()
	job.Status = types.CachingStatusActive
	job.Details.SourceSizeBytes = sourceSize
	job.Details.CacheSizeBytes = sourceSize // Initially cache all data
	now := time.Now()
	job.Details.UpdatedAt = &now
	cc.cachesMux.Unlock()

	log.Printf("Cache %s: Active (size: %d bytes)", job.ID, sourceSize)

	// Start statistics collection loop
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

// updateCacheStats updates cache statistics (simulated)
func (cc *CachingController) updateCacheStats(job *CacheJob) {
	cc.cachesMux.Lock()
	defer cc.cachesMux.Unlock()

	if job.Details.Stats == nil {
		return
	}

	// Simulate statistics (in real implementation, get from Manta)
	job.Details.Stats.TotalRequests += int64(100 + time.Now().Unix()%50)
	job.Details.Stats.CacheHits += int64(80 + time.Now().Unix()%40)
	job.Details.Stats.CacheMisses = job.Details.Stats.TotalRequests - job.Details.Stats.CacheHits

	if job.Details.Stats.TotalRequests > 0 {
		job.Details.Stats.HitRatio = float64(job.Details.Stats.CacheHits) / float64(job.Details.Stats.TotalRequests)
	}

	// Simulate I/O statistics based on tier
	switch job.Request.TargetTier {
	case types.TierNVMe:
		job.Details.Stats.ReadThroughputMBps = 3000 + int64(time.Now().Unix()%500)
		job.Details.Stats.WriteThroughputMBps = 2000 + int64(time.Now().Unix()%300)
		job.Details.Stats.IOPS = 500000 + int64(time.Now().Unix()%100000)
		job.Details.Stats.AvgReadLatencyUs = 50 + int64(time.Now().Unix()%20)
		job.Details.Stats.AvgWriteLatencyUs = 80 + int64(time.Now().Unix()%30)
	case types.TierSSD:
		job.Details.Stats.ReadThroughputMBps = 500 + int64(time.Now().Unix()%100)
		job.Details.Stats.WriteThroughputMBps = 400 + int64(time.Now().Unix()%80)
		job.Details.Stats.IOPS = 100000 + int64(time.Now().Unix()%20000)
		job.Details.Stats.AvgReadLatencyUs = 200 + int64(time.Now().Unix()%50)
		job.Details.Stats.AvgWriteLatencyUs = 300 + int64(time.Now().Unix()%80)
	case types.TierHDD:
		job.Details.Stats.ReadThroughputMBps = 150 + int64(time.Now().Unix()%30)
		job.Details.Stats.WriteThroughputMBps = 100 + int64(time.Now().Unix()%20)
		job.Details.Stats.IOPS = 200 + int64(time.Now().Unix()%50)
		job.Details.Stats.AvgReadLatencyUs = 5000 + int64(time.Now().Unix()%1000)
		job.Details.Stats.AvgWriteLatencyUs = 8000 + int64(time.Now().Unix()%2000)
	}

	now := time.Now()
	job.Details.Stats.LastAccessTime = &now
}

// performEviction performs cache eviction
func (cc *CachingController) performEviction(job *CacheJob) {
	log.Printf("Cache %s: Starting eviction", job.ID)

	// Simulate eviction process
	select {
	case <-job.ctx.Done():
		return
	case <-time.After(1 * time.Second):
	}

	cc.cachesMux.Lock()
	if job.Details.Stats != nil {
		job.Details.Stats.EvictedDataBytes += job.Details.CacheSizeBytes
	}
	job.Details.CacheSizeBytes = 0
	job.Status = types.CachingStatusInactive
	now := time.Now()
	job.Details.UpdatedAt = &now
	if job.Details.Stats != nil {
		job.Details.Stats.LastEvictionTime = &now
	}
	cc.cachesMux.Unlock()

	log.Printf("Cache %s: Eviction completed", job.ID)
}

// performTierMigration migrates cache data between tiers
func (cc *CachingController) performTierMigration(job *CacheJob, oldTier, newTier types.StorageTier) {
	log.Printf("Cache %s: Migrating from %s to %s", job.ID, oldTier, newTier)

	// Simulate migration process
	select {
	case <-job.ctx.Done():
		return
	case <-time.After(3 * time.Second):
	}

	cc.cachesMux.Lock()
	now := time.Now()
	job.Details.UpdatedAt = &now
	cc.cachesMux.Unlock()

	log.Printf("Cache %s: Tier migration completed (%s -> %s)", job.ID, oldTier, newTier)
}

// performWarmup pre-loads data into cache
func (cc *CachingController) performWarmup(job *CacheJob, paths []string, pattern string) {
	log.Printf("Cache %s: Starting warmup (paths: %v, pattern: %s)", job.ID, paths, pattern)

	// Simulate warmup process
	select {
	case <-job.ctx.Done():
		return
	case <-time.After(2 * time.Second):
	}

	cc.cachesMux.Lock()
	if job.Details.Stats != nil {
		// Simulate data loaded
		job.Details.Stats.LoadedDataBytes += 1024 * 1024 * 100 // 100MB
	}
	now := time.Now()
	job.Details.UpdatedAt = &now
	cc.cachesMux.Unlock()

	log.Printf("Cache %s: Warmup completed", job.ID)
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
