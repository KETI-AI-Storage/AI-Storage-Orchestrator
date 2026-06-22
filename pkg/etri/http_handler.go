package etri

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// HTTPHandler provides REST endpoints under /api/v1/etri.
type HTTPHandler struct {
	svc *Service
}

// NewHTTPHandler creates a new HTTP handler for ETRI APIs.
func NewHTTPHandler(svc *Service) *HTTPHandler {
	return &HTTPHandler{svc: svc}
}

// RegisterRoutes mounts ETRI routes under the given router group.
func (h *HTTPHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/workloads/metadata", h.registerWorkloadMetadata)
	rg.GET("/workloads/:namespace/:name/status", h.getWorkloadStatus)
	rg.POST("/workloads/result", h.reportWorkloadResult)
	// Additional APIs like dataset binding can be added later:
	// rg.GET("/datasets/:name/binding", h.getDatasetBindingInfo)
}

// registerWorkloadMetadata handles:
// POST /api/v1/etri/workloads/metadata
func (h *HTTPHandler) registerWorkloadMetadata(c *gin.Context) {
	var meta EtriWorkloadMetadata
	if err := c.ShouldBindJSON(&meta); err != nil {
		log.Printf("[ETRI][METADATA][ERROR] failed to bind request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid request format",
			"details": err.Error(),
		})
		return
	}

	log.Printf("[ETRI][METADATA][REQUEST] workload_name=%s namespace=%s stage=%s framework=%s datasets=%d",
		meta.WorkloadName, meta.Namespace, meta.Stage, meta.Framework, len(meta.Datasets))

	if err := h.svc.RegisterWorkloadMetadata(c.Request.Context(), &meta); err != nil {
		log.Printf("[ETRI][METADATA][ERROR] failed to register workload metadata: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "failed to register workload metadata",
			"details": err.Error(),
		})
		return
	}

	log.Printf("[ETRI][METADATA][OK] registered workload_name=%s namespace=%s", meta.WorkloadName, meta.Namespace)

	c.JSON(http.StatusAccepted, gin.H{
		"message":       "workload metadata registered",
		"workload_name": meta.WorkloadName,
		"namespace":     meta.Namespace,
	})
}

// getWorkloadStatus handles:
// GET /api/v1/etri/workloads/{namespace}/{name}/status
func (h *HTTPHandler) getWorkloadStatus(c *gin.Context) {
	ref := WorkloadRef{
		Namespace:    c.Param("namespace"),
		WorkloadName: c.Param("name"),
	}

	log.Printf("[ETRI][STATUS][REQUEST] workload_name=%s namespace=%s", ref.WorkloadName, ref.Namespace)

	status, err := h.svc.GetWorkloadStatus(c.Request.Context(), ref)
	if err != nil {
		log.Printf("[ETRI][STATUS][ERROR] failed to get status for workload_name=%s namespace=%s: %v",
			ref.WorkloadName, ref.Namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to get workload status",
			"details": err.Error(),
		})
		return
	}
	if status == nil {
		log.Printf("[ETRI][STATUS][MISS] no status found for workload_name=%s namespace=%s",
			ref.WorkloadName, ref.Namespace)
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "workload status not found",
			"details": "no status or metadata found for the given reference",
		})
		return
	}

	log.Printf("[ETRI][STATUS][OK] workload_name=%s namespace=%s phase=%s node_name=%s locality=%s dataset_name=%s",
		status.WorkloadName, status.Namespace, status.Phase, status.NodeName, status.Locality, status.DatasetName)

	c.JSON(http.StatusOK, status)
}

// reportWorkloadResult handles:
// POST /api/v1/etri/workloads/result
func (h *HTTPHandler) reportWorkloadResult(c *gin.Context) {
	var status WorkloadStatusResponse
	if err := c.ShouldBindJSON(&status); err != nil {
		log.Printf("[ETRI][RESULT][ERROR] failed to bind result: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid request format",
			"details": err.Error(),
		})
		return
	}

	log.Printf("[ETRI][RESULT][REQUEST] workload_name=%s namespace=%s phase=%s node_name=%s locality=%s dataset_name=%s",
		status.WorkloadName, status.Namespace, status.Phase, status.NodeName, status.Locality, status.DatasetName)

	if err := h.svc.UpdateWorkloadExecutionResult(c.Request.Context(), &status); err != nil {
		log.Printf("[ETRI][RESULT][ERROR] failed to update workload result: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "failed to update workload result",
			"details": err.Error(),
		})
		return
	}

	log.Printf("[ETRI][RESULT][OK] updated workload_name=%s namespace=%s phase=%s",
		status.WorkloadName, status.Namespace, status.Phase)

	c.JSON(http.StatusOK, gin.H{
		"message":       "workload result updated",
		"workload_name": status.WorkloadName,
		"namespace":     status.Namespace,
		"phase":         status.Phase,
	})
}

