package api

import (
	"net/http"

	"ai-storage-orchestrator/pkg/types"

	"github.com/gin-gonic/gin"
)

// LoadbalancingHandlers contains HTTP handlers for loadbalancing operations
type LoadbalancingHandlers struct {
	controller LoadbalancingController
}

// LoadbalancingController interface for dependency injection
type LoadbalancingController interface {
	StartLoadbalancing(req *types.LoadbalancingRequest) (string, error)
	GetLoadbalancingJob(jobID string) (*types.LoadbalancingResponse, error)
	ListLoadbalancingJobs() []*types.LoadbalancingResponse
	CancelLoadbalancing(jobID string) error
	GetMetrics() *types.LoadbalancingMetrics
}

// NewLoadbalancingHandlers creates a new loadbalancing handlers instance
func NewLoadbalancingHandlers(controller LoadbalancingController) *LoadbalancingHandlers {
	return &LoadbalancingHandlers{
		controller: controller,
	}
}

// StartLoadbalancing handles POST /api/v1/loadbalancing
// @Summary Start a new loadbalancing job
// @Description Initiates a new loadbalancing operation to redistribute pods across nodes
// @Tags Loadbalancing
// @Accept json
// @Produce json
// @Param request body types.LoadbalancingRequest true "Loadbalancing Request"
// @Success 201 {object} types.LoadbalancingResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/loadbalancing [post]
func (h *LoadbalancingHandlers) StartLoadbalancing(c *gin.Context) {
	var req types.LoadbalancingRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request format",
			Message: err.Error(),
		})
		return
	}

	jobID, err := h.controller.StartLoadbalancing(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to start loadbalancing",
			Message: err.Error(),
		})
		return
	}

	// Get job details
	response, err := h.controller.GetLoadbalancingJob(jobID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Loadbalancing started but failed to retrieve details",
			Message: err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, response)
}

// GetLoadbalancing handles GET /api/v1/loadbalancing/:id
// @Summary Get loadbalancing job details
// @Description Retrieves detailed information about a specific loadbalancing job
// @Tags Loadbalancing
// @Produce json
// @Param id path string true "Loadbalancing Job ID"
// @Success 200 {object} types.LoadbalancingResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/loadbalancing/{id} [get]
func (h *LoadbalancingHandlers) GetLoadbalancing(c *gin.Context) {
	jobID := c.Param("id")

	response, err := h.controller.GetLoadbalancingJob(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:   "Loadbalancing job not found",
			Message: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// ListLoadbalancing handles GET /api/v1/loadbalancing
// @Summary List all loadbalancing jobs
// @Description Retrieves a list of all loadbalancing jobs
// @Tags Loadbalancing
// @Produce json
// @Success 200 {array} types.LoadbalancingResponse
// @Router /api/v1/loadbalancing [get]
func (h *LoadbalancingHandlers) ListLoadbalancing(c *gin.Context) {
	jobs := h.controller.ListLoadbalancingJobs()
	c.JSON(http.StatusOK, jobs)
}

// CancelLoadbalancing handles DELETE /api/v1/loadbalancing/:id
// @Summary Cancel a loadbalancing job
// @Description Cancels a running loadbalancing job
// @Tags Loadbalancing
// @Produce json
// @Param id path string true "Loadbalancing Job ID"
// @Success 200 {object} SuccessResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/loadbalancing/{id} [delete]
func (h *LoadbalancingHandlers) CancelLoadbalancing(c *gin.Context) {
	jobID := c.Param("id")

	if err := h.controller.CancelLoadbalancing(jobID); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Failed to cancel loadbalancing",
			Message: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Loadbalancing job cancelled successfully",
	})
}

// GetLoadbalancingMetrics handles GET /api/v1/loadbalancing/metrics
// @Summary Get loadbalancing metrics
// @Description Retrieves overall loadbalancing system metrics
// @Tags Loadbalancing
// @Produce json
// @Success 200 {object} types.LoadbalancingMetrics
// @Router /api/v1/loadbalancing/metrics [get]
func (h *LoadbalancingHandlers) GetLoadbalancingMetrics(c *gin.Context) {
	metrics := h.controller.GetMetrics()
	c.JSON(http.StatusOK, metrics)
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// SuccessResponse represents a successful operation
type SuccessResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}
