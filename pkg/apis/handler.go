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
	migrationController   *controller.MigrationController
	autoscalingController *controller.AutoscalingController
}

// NewHandler creates a new API handler
func NewHandler(migrationController *controller.MigrationController, autoscalingController *controller.AutoscalingController) *Handler {
	return &Handler{
		migrationController:   migrationController,
		autoscalingController: autoscalingController,
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
