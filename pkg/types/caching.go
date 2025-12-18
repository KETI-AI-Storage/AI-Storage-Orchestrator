package types

import "time"

// CachingRequest represents a request to cache data for AI workloads
// 글로벌 캐싱: Manta 스토리지 티어 간 데이터 캐싱
type CachingRequest struct {
	// SourcePVC is the PVC containing data to cache
	SourcePVC string `json:"source_pvc" binding:"required"`

	// SourceNamespace is the namespace of the source PVC
	SourceNamespace string `json:"source_namespace" binding:"required"`

	// SourcePath is the path within the PVC to cache (optional, default: "/")
	SourcePath string `json:"source_path,omitempty"`

	// TargetTier is the storage tier to cache data to
	// Options: "nvme" (fastest), "ssd" (fast), "hdd" (slow), "auto" (policy decides)
	TargetTier StorageTier `json:"target_tier" binding:"required"`

	// CacheSize is the maximum cache size (e.g., "10Gi", "100Gi")
	CacheSize string `json:"cache_size,omitempty"`

	// CachePolicy defines the eviction policy
	// Options: "lru" (Least Recently Used), "lfu" (Least Frequently Used), "fifo", "ttl"
	CachePolicy CacheEvictionPolicy `json:"cache_policy,omitempty"` // default: "lru"

	// TTL is time-to-live for cached data (only used with "ttl" policy)
	TTLSeconds int64 `json:"ttl_seconds,omitempty"` // default: 3600 (1 hour)

	// Priority determines cache priority (higher = more important)
	Priority int32 `json:"priority,omitempty"` // default: 0

	// Prefetch enables proactive data loading before it's needed
	Prefetch bool `json:"prefetch,omitempty"`

	// WorkloadSelector targets specific workloads that will use this cache
	WorkloadSelector *WorkloadSelector `json:"workload_selector,omitempty"`

	// Reason for caching (for auditing)
	Reason string `json:"reason,omitempty"`
}

// WorkloadSelector selects workloads that will benefit from this cache
type WorkloadSelector struct {
	// Namespace to target
	Namespace string `json:"namespace,omitempty"`

	// Labels to match workloads
	Labels map[string]string `json:"labels,omitempty"`

	// WorkloadNames are specific workload names
	WorkloadNames []string `json:"workload_names,omitempty"`
}

// StorageTier represents a storage tier in Manta cluster
type StorageTier string

const (
	// TierNVMe is NVMe-based high-performance storage
	TierNVMe StorageTier = "nvme"

	// TierSSD is SSD-based fast storage
	TierSSD StorageTier = "ssd"

	// TierHDD is HDD-based high-capacity storage
	TierHDD StorageTier = "hdd"

	// TierS3 is S3 object-based archiving storage
	TierS3 StorageTier = "s3"

	// TierAuto lets the policy engine decide the best tier
	TierAuto StorageTier = "auto"
)

// CacheEvictionPolicy defines how cache entries are evicted
type CacheEvictionPolicy string

const (
	// PolicyLRU evicts least recently used entries
	PolicyLRU CacheEvictionPolicy = "lru"

	// PolicyLFU evicts least frequently used entries
	PolicyLFU CacheEvictionPolicy = "lfu"

	// PolicyFIFO evicts oldest entries first
	PolicyFIFO CacheEvictionPolicy = "fifo"

	// PolicyTTL evicts entries after time-to-live expires
	PolicyTTL CacheEvictionPolicy = "ttl"
)

// CachingResponse represents the response after initiating caching
type CachingResponse struct {
	CacheID string         `json:"cache_id"`
	Status  CachingStatus  `json:"status"`
	Message string         `json:"message,omitempty"`
	Details *CacheDetails  `json:"details,omitempty"`
}

// CachingStatus represents the status of a caching operation
type CachingStatus string

const (
	CachingStatusPending    CachingStatus = "pending"
	CachingStatusLoading    CachingStatus = "loading"    // Data being loaded into cache
	CachingStatusActive     CachingStatus = "active"     // Cache is active and serving
	CachingStatusEvicting   CachingStatus = "evicting"   // Cache is being evicted
	CachingStatusInactive   CachingStatus = "inactive"   // Cache is disabled
	CachingStatusFailed     CachingStatus = "failed"
)

// CacheDetails contains detailed information about a cache
type CacheDetails struct {
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`

	// Source information
	SourcePVC       string `json:"source_pvc"`
	SourceNamespace string `json:"source_namespace"`
	SourcePath      string `json:"source_path"`
	SourceSizeBytes int64  `json:"source_size_bytes"`

	// Cache configuration
	TargetTier    StorageTier         `json:"target_tier"`
	CachePolicy   CacheEvictionPolicy `json:"cache_policy"`
	CacheSizeBytes int64              `json:"cache_size_bytes"`
	TTLSeconds    int64               `json:"ttl_seconds,omitempty"`
	Priority      int32               `json:"priority"`

	// Cache statistics
	Stats *CacheStats `json:"stats,omitempty"`

	// Error message if failed
	ErrorMessage string `json:"error_message,omitempty"`
}

// CacheStats contains cache performance statistics
type CacheStats struct {
	// Hit/Miss statistics
	TotalRequests   int64   `json:"total_requests"`
	CacheHits       int64   `json:"cache_hits"`
	CacheMisses     int64   `json:"cache_misses"`
	HitRatio        float64 `json:"hit_ratio"` // cache_hits / total_requests

	// Data statistics
	CachedDataBytes   int64 `json:"cached_data_bytes"`   // Current cached data size
	EvictedDataBytes  int64 `json:"evicted_data_bytes"`  // Total evicted data
	LoadedDataBytes   int64 `json:"loaded_data_bytes"`   // Total loaded data

	// I/O statistics
	ReadThroughputMBps  int64 `json:"read_throughput_mbps"`  // Current read throughput
	WriteThroughputMBps int64 `json:"write_throughput_mbps"` // Current write throughput
	IOPS                int64 `json:"iops"`                  // Current IOPS

	// Latency statistics (microseconds)
	AvgReadLatencyUs  int64 `json:"avg_read_latency_us"`
	AvgWriteLatencyUs int64 `json:"avg_write_latency_us"`

	// Time statistics
	LastAccessTime    *time.Time `json:"last_access_time,omitempty"`
	LastEvictionTime  *time.Time `json:"last_eviction_time,omitempty"`
}

// CachingMetrics contains overall caching system metrics
type CachingMetrics struct {
	TotalCaches       int64   `json:"total_caches"`
	ActiveCaches      int64   `json:"active_caches"`
	TotalCachedBytes  int64   `json:"total_cached_bytes"`

	// Aggregated hit ratio across all caches
	GlobalHitRatio    float64 `json:"global_hit_ratio"`

	// Tier distribution
	NVMeCacheCount    int64   `json:"nvme_cache_count"`
	SSDCacheCount     int64   `json:"ssd_cache_count"`
	HDDCacheCount     int64   `json:"hdd_cache_count"`

	// Performance metrics
	TotalReadThroughputMBps  int64 `json:"total_read_throughput_mbps"`
	TotalWriteThroughputMBps int64 `json:"total_write_throughput_mbps"`
	TotalIOPS                int64 `json:"total_iops"`

	// Savings estimation
	EstimatedIOSavedBytes int64 `json:"estimated_io_saved_bytes"`
	EstimatedTimeSavedMs  int64 `json:"estimated_time_saved_ms"`
}

// TierMigrationRequest represents a request to migrate data between tiers
// 티어 마이그레이션: 데이터를 다른 스토리지 계층으로 이동
type TierMigrationRequest struct {
	// CacheID is the cache to migrate
	CacheID string `json:"cache_id" binding:"required"`

	// TargetTier is the new storage tier
	TargetTier StorageTier `json:"target_tier" binding:"required"`

	// Reason for migration
	Reason string `json:"reason,omitempty"`
}

// CacheWarmupRequest represents a request to pre-warm cache
// 캐시 프리페치: 데이터를 미리 캐시에 로드
type CacheWarmupRequest struct {
	// CacheID is the cache to warm up
	CacheID string `json:"cache_id" binding:"required"`

	// Paths are specific paths to prefetch (optional)
	Paths []string `json:"paths,omitempty"`

	// Pattern is a glob pattern to match files (e.g., "*.tfrecord")
	Pattern string `json:"pattern,omitempty"`

	// Async runs warmup in background
	Async bool `json:"async,omitempty"`
}

// CachePolicyDecision represents a decision from the policy engine
// 정책 엔진으로부터 받은 캐싱 결정
type CachePolicyDecision struct {
	// Action to take
	Action CachePolicyAction `json:"action"`

	// TargetCacheID is the cache to act on
	TargetCacheID string `json:"target_cache_id,omitempty"`

	// TargetTier for tier migration
	TargetTier StorageTier `json:"target_tier,omitempty"`

	// Priority of this decision
	Priority int32 `json:"priority"`

	// Probability from the policy model
	Probability float64 `json:"probability"`

	// Horizon is the prediction time window
	Horizon string `json:"horizon"` // e.g., "30min", "60min"

	// Reason for the decision
	Reason string `json:"reason"`
}

// CachePolicyAction defines actions the policy engine can decide
type CachePolicyAction string

const (
	// ActionCreateCache creates a new cache
	ActionCreateCache CachePolicyAction = "create_cache"

	// ActionEvictCache evicts an existing cache
	ActionEvictCache CachePolicyAction = "evict_cache"

	// ActionMigrateTier migrates cache to different tier
	ActionMigrateTier CachePolicyAction = "migrate_tier"

	// ActionWarmupCache pre-warms cache with data
	ActionWarmupCache CachePolicyAction = "warmup_cache"

	// ActionNoAction takes no action
	ActionNoAction CachePolicyAction = "no_action"
)
