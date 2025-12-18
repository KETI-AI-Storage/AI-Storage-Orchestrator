package apis

import (
	"fmt"
	"net/http"

	"ai-storage-orchestrator/pkg/controller"
	"ai-storage-orchestrator/pkg/types"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP API endpoints for the migration orchestrator
type Handler struct {
	migrationController     *controller.MigrationController
	autoscalingController   *controller.AutoscalingController
	loadbalancingController *controller.LoadbalancingController
	provisioningController  *controller.ProvisioningController
	preemptionController    *controller.PreemptionController
	cachingController       *controller.CachingController
}

// NewHandler creates a new API handler
func NewHandler(migrationController *controller.MigrationController, autoscalingController *controller.AutoscalingController, loadbalancingController *controller.LoadbalancingController, provisioningController *controller.ProvisioningController, preemptionController *controller.PreemptionController, cachingController *controller.CachingController) *Handler {
	return &Handler{
		migrationController:     migrationController,
		autoscalingController:   autoscalingController,
		loadbalancingController: loadbalancingController,
		provisioningController:  provisioningController,
		preemptionController:    preemptionController,
		cachingController:       cachingController,
	}
}

// SetupRoutes configures the HTTP routes
func (h *Handler) SetupRoutes() *gin.Engine {
	router := gin.Default()
	
	// Add middleware
	router.Use(gin.Logger())
	router.Use(gin.Recovery())
	router.Use(corsMiddleware())

	// Health check endpoint
	router.GET("/health", h.healthCheck)

	// Migration API endpoints
	v1 := router.Group("/api/v1")
	{
		v1.POST("/migrations", h.createMigration)
		v1.GET("/migrations/:id", h.getMigration)
		v1.GET("/migrations/:id/status", h.getMigrationStatus)
		v1.GET("/metrics", h.getMetrics)

		// Autoscaling API endpoints
		v1.POST("/autoscaling", h.createAutoscaler)
		v1.GET("/autoscaling/:id", h.getAutoscaler)
		v1.DELETE("/autoscaling/:id", h.deleteAutoscaler)
		v1.GET("/autoscaling", h.listAutoscalers)
		v1.GET("/autoscaling/metrics", h.getAutoscalingMetrics)

		// Loadbalancing API endpoints
		v1.POST("/loadbalancing", h.createLoadbalancing)
		v1.GET("/loadbalancing/:id", h.getLoadbalancing)
		v1.DELETE("/loadbalancing/:id", h.cancelLoadbalancing)
		v1.GET("/loadbalancing", h.listLoadbalancing)
		v1.GET("/loadbalancing/metrics", h.getLoadbalancingMetrics)

		// Provisioning API endpoints
		v1.POST("/provisioning", h.createProvisioning)
		v1.GET("/provisioning/:id", h.getProvisioning)
		v1.DELETE("/provisioning/:id", h.deleteProvisioning)
		v1.GET("/provisioning", h.listProvisioning)
		v1.GET("/provisioning/recommend/:workload_type", h.getProvisioningRecommendation)
		v1.GET("/provisioning/metrics", h.getProvisioningMetrics)

		// Preemption API endpoints
		v1.POST("/preemption", h.createPreemption)
		v1.GET("/preemption/:id", h.getPreemption)
		v1.GET("/preemption", h.listPreemptions)
		v1.GET("/preemption/metrics", h.getPreemptionMetrics)

		// Caching API endpoints (글로벌 캐싱)
		v1.POST("/caching", h.createCache)
		v1.GET("/caching/:id", h.getCache)
		v1.DELETE("/caching/:id", h.deleteCache)
		v1.GET("/caching", h.listCaches)
		v1.POST("/caching/:id/evict", h.evictCache)
		v1.POST("/caching/:id/warmup", h.warmupCache)
		v1.POST("/caching/:id/migrate", h.migrateCache)
		v1.POST("/caching/policy", h.applyPolicyDecision)
		v1.GET("/caching/metrics", h.getCachingMetrics)
	}

	return router
}

// healthCheck provides a simple health check endpoint
func (h *Handler) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "ai-storage-orchestrator",
		"version": "1.0.0",
	})
}

// createMigration handles POST /api/v1/migrations
func (h *Handler) createMigration(c *gin.Context) {
	var req types.MigrationRequest
	
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}

	// Validate required fields
	if err := h.validateMigrationRequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Validation failed",
			"details": err.Error(),
		})
		return
	}

	// Set default timeout if not provided
	if req.Timeout == 0 {
		req.Timeout = 600 // 10 minutes default
	}

	// Start migration
	response, err := h.migrationController.StartMigration(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to start migration",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusAccepted, response)
}

// getMigration handles GET /api/v1/migrations/:id
func (h *Handler) getMigration(c *gin.Context) {
	migrationID := c.Param("id")
	
	response, err := h.migrationController.GetMigrationStatus(migrationID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Migration not found",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// getMigrationStatus handles GET /api/v1/migrations/:id/status
func (h *Handler) getMigrationStatus(c *gin.Context) {
	migrationID := c.Param("id")
	
	response, err := h.migrationController.GetMigrationStatus(migrationID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Migration not found",
			"details": err.Error(),
		})
		return
	}

	// Return simplified status response
	statusResponse := gin.H{
		"migration_id": response.MigrationID,
		"status":       response.Status,
		"message":      response.Message,
	}

	// Add timing information if available
	if response.Details != nil {
		statusResponse["start_time"] = response.Details.StartTime
		if response.Details.EndTime != nil {
			statusResponse["end_time"] = response.Details.EndTime
		}
		if response.Details.Duration != nil {
			statusResponse["duration_seconds"] = response.Details.Duration.Seconds()
		}
	}

	c.JSON(http.StatusOK, statusResponse)
}

// getMetrics handles GET /api/v1/metrics
func (h *Handler) getMetrics(c *gin.Context) {
	metrics := h.migrationController.GetMetrics()
	c.JSON(http.StatusOK, metrics)
}

// validateMigrationRequest validates the migration request
func (h *Handler) validateMigrationRequest(req *types.MigrationRequest) error {
	if req.PodName == "" {
		return fmt.Errorf("pod_name is required")
	}
	if req.PodNamespace == "" {
		return fmt.Errorf("pod_namespace is required")
	}
	if req.SourceNode == "" {
		return fmt.Errorf("source_node is required")
	}
	if req.TargetNode == "" {
		return fmt.Errorf("target_node is required")
	}
	if req.SourceNode == req.TargetNode {
		return fmt.Errorf("source_node and target_node cannot be the same")
	}
	if req.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}
	
	return nil
}

// createAutoscaler handles POST /api/v1/autoscaling
func (h *Handler) createAutoscaler(c *gin.Context) {
	var req types.AutoscalingRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}

	response, err := h.autoscalingController.CreateAutoscaler(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to create autoscaler",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, response)
}

// getAutoscaler handles GET /api/v1/autoscaling/:id
func (h *Handler) getAutoscaler(c *gin.Context) {
	autoscalerID := c.Param("id")

	response, err := h.autoscalingController.GetAutoscaler(autoscalerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Autoscaler not found",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// deleteAutoscaler handles DELETE /api/v1/autoscaling/:id
func (h *Handler) deleteAutoscaler(c *gin.Context) {
	autoscalerID := c.Param("id")

	err := h.autoscalingController.DeleteAutoscaler(autoscalerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Failed to delete autoscaler",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Autoscaler deleted successfully",
		"autoscaler_id": autoscalerID,
	})
}

// listAutoscalers handles GET /api/v1/autoscaling
func (h *Handler) listAutoscalers(c *gin.Context) {
	autoscalers := h.autoscalingController.ListAutoscalers()
	c.JSON(http.StatusOK, gin.H{
		"autoscalers": autoscalers,
		"count":       len(autoscalers),
	})
}

// getAutoscalingMetrics handles GET /api/v1/autoscaling/metrics
func (h *Handler) getAutoscalingMetrics(c *gin.Context) {
	metrics := h.autoscalingController.GetMetrics()
	c.JSON(http.StatusOK, metrics)
}

// corsMiddleware provides CORS support
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept-Encoding, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// ============================================================================
// Loadbalancing Handlers
// ============================================================================

// createLoadbalancing handles POST /api/v1/loadbalancing
func (h *Handler) createLoadbalancing(c *gin.Context) {
	var req types.LoadbalancingRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}

	jobID, err := h.loadbalancingController.StartLoadbalancing(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to start loadbalancing",
			"details": err.Error(),
		})
		return
	}

	// Get job details
	response, err := h.loadbalancingController.GetLoadbalancingJob(jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Loadbalancing started but failed to retrieve details",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, response)
}

// getLoadbalancing handles GET /api/v1/loadbalancing/:id
func (h *Handler) getLoadbalancing(c *gin.Context) {
	jobID := c.Param("id")

	response, err := h.loadbalancingController.GetLoadbalancingJob(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Loadbalancing job not found",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// cancelLoadbalancing handles DELETE /api/v1/loadbalancing/:id
func (h *Handler) cancelLoadbalancing(c *gin.Context) {
	jobID := c.Param("id")

	if err := h.loadbalancingController.CancelLoadbalancing(jobID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Failed to cancel loadbalancing",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Loadbalancing job cancelled successfully",
		"job_id":  jobID,
	})
}

// listLoadbalancing handles GET /api/v1/loadbalancing
func (h *Handler) listLoadbalancing(c *gin.Context) {
	jobs := h.loadbalancingController.ListLoadbalancingJobs()
	c.JSON(http.StatusOK, gin.H{
		"loadbalancing_jobs": jobs,
		"count":              len(jobs),
	})
}

// getLoadbalancingMetrics handles GET /api/v1/loadbalancing/metrics
func (h *Handler) getLoadbalancingMetrics(c *gin.Context) {
	metrics := h.loadbalancingController.GetMetrics()
	c.JSON(http.StatusOK, metrics)
}
// ========================================
// Provisioning API Handlers
// ========================================

// createProvisioning handles POST /api/v1/provisioning
func (h *Handler) createProvisioning(c *gin.Context) {
	var req types.ProvisioningRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}

	response, err := h.provisioningController.CreateProvisioning(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Failed to create provisioning",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, response)
}

// getProvisioning handles GET /api/v1/provisioning/:id
func (h *Handler) getProvisioning(c *gin.Context) {
	provisioningID := c.Param("id")

	response, err := h.provisioningController.GetProvisioning(provisioningID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Provisioning not found",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// deleteProvisioning handles DELETE /api/v1/provisioning/:id
func (h *Handler) deleteProvisioning(c *gin.Context) {
	provisioningID := c.Param("id")

	if err := h.provisioningController.DeleteProvisioning(provisioningID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Failed to delete provisioning",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":         "Provisioning deleted successfully",
		"provisioning_id": provisioningID,
	})
}

// listProvisioning handles GET /api/v1/provisioning
func (h *Handler) listProvisioning(c *gin.Context) {
	provisionings := h.provisioningController.ListProvisionings()
	c.JSON(http.StatusOK, gin.H{
		"provisionings": provisionings,
		"count":         len(provisionings),
	})
}

// getProvisioningRecommendation handles GET /api/v1/provisioning/recommend/:workload_type
func (h *Handler) getProvisioningRecommendation(c *gin.Context) {
	workloadType := c.Param("workload_type")

	recommendation := h.provisioningController.GetRecommendation(workloadType)
	c.JSON(http.StatusOK, recommendation)
}

// getProvisioningMetrics handles GET /api/v1/provisioning/metrics
func (h *Handler) getProvisioningMetrics(c *gin.Context) {
	metrics := h.provisioningController.GetMetrics()
	c.JSON(http.StatusOK, metrics)
}

// ========================================
// Preemption API Handlers
// ========================================

// createPreemption handles POST /api/v1/preemption
func (h *Handler) createPreemption(c *gin.Context) {
	var req types.PreemptionRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}

	response, err := h.preemptionController.StartPreemption(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Failed to start preemption",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, response)
}

// getPreemption handles GET /api/v1/preemption/:id
func (h *Handler) getPreemption(c *gin.Context) {
	preemptionID := c.Param("id")

	response, err := h.preemptionController.GetPreemption(preemptionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Preemption job not found",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// listPreemptions handles GET /api/v1/preemption
func (h *Handler) listPreemptions(c *gin.Context) {
	preemptions := h.preemptionController.ListPreemptions()
	c.JSON(http.StatusOK, gin.H{
		"preemptions": preemptions,
		"count":       len(preemptions),
	})
}

// getPreemptionMetrics handles GET /api/v1/preemption/metrics
func (h *Handler) getPreemptionMetrics(c *gin.Context) {
	metrics := h.preemptionController.GetMetrics()
	c.JSON(http.StatusOK, metrics)
}

// ========================================
// Caching API Handlers (글로벌 캐싱)
// ========================================

// createCache handles POST /api/v1/caching
func (h *Handler) createCache(c *gin.Context) {
	var req types.CachingRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}

	response, err := h.cachingController.CreateCache(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Failed to create cache",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, response)
}

// getCache handles GET /api/v1/caching/:id
func (h *Handler) getCache(c *gin.Context) {
	cacheID := c.Param("id")

	response, err := h.cachingController.GetCache(cacheID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Cache not found",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// deleteCache handles DELETE /api/v1/caching/:id
func (h *Handler) deleteCache(c *gin.Context) {
	cacheID := c.Param("id")

	if err := h.cachingController.DeleteCache(cacheID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Failed to delete cache",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "Cache deleted successfully",
		"cache_id": cacheID,
	})
}

// listCaches handles GET /api/v1/caching
func (h *Handler) listCaches(c *gin.Context) {
	caches := h.cachingController.ListCaches()
	c.JSON(http.StatusOK, gin.H{
		"caches": caches,
		"count":  len(caches),
	})
}

// evictCache handles POST /api/v1/caching/:id/evict
func (h *Handler) evictCache(c *gin.Context) {
	cacheID := c.Param("id")

	if err := h.cachingController.EvictCache(cacheID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Failed to evict cache",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "Cache eviction started",
		"cache_id": cacheID,
	})
}

// warmupCache handles POST /api/v1/caching/:id/warmup
func (h *Handler) warmupCache(c *gin.Context) {
	cacheID := c.Param("id")

	var req types.CacheWarmupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Use default warmup if no body provided
		req = types.CacheWarmupRequest{
			CacheID: cacheID,
			Async:   true,
		}
	} else {
		req.CacheID = cacheID
	}

	if err := h.cachingController.WarmupCache(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Failed to warmup cache",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "Cache warmup started",
		"cache_id": cacheID,
		"async":    req.Async,
	})
}

// migrateCache handles POST /api/v1/caching/:id/migrate
func (h *Handler) migrateCache(c *gin.Context) {
	cacheID := c.Param("id")

	var req types.TierMigrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}
	req.CacheID = cacheID

	if err := h.cachingController.MigrateTier(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Failed to migrate cache tier",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "Cache tier migration started",
		"cache_id":    cacheID,
		"target_tier": req.TargetTier,
	})
}

// applyPolicyDecision handles POST /api/v1/caching/policy
// 정책 엔진으로부터 받은 캐싱 결정 적용
func (h *Handler) applyPolicyDecision(c *gin.Context) {
	var decision types.CachePolicyDecision

	if err := c.ShouldBindJSON(&decision); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request format",
			"details": err.Error(),
		})
		return
	}

	if err := h.cachingController.ApplyPolicyDecision(&decision); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Failed to apply policy decision",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Policy decision applied successfully",
		"action":  decision.Action,
	})
}

// getCachingMetrics handles GET /api/v1/caching/metrics
func (h *Handler) getCachingMetrics(c *gin.Context) {
	metrics := h.cachingController.GetMetrics()
	c.JSON(http.StatusOK, metrics)
}
