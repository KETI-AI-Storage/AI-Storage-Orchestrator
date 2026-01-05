package controller

import (
	"fmt"
	"log"
	"sync"
	"time"

	"ai-storage-orchestrator/pkg/types"
)

// InsightController handles workload signature reports from insight-trace sidecars
type InsightController struct {
	// Store active workload signatures
	signatures map[string]*types.WorkloadSignature
	mu         sync.RWMutex

	// Metrics
	totalReports       int64
	reportsByType      map[string]int64
	reportsByNamespace map[string]int64
	lastReportTime     time.Time
}

// NewInsightController creates a new insight controller
func NewInsightController() *InsightController {
	return &InsightController{
		signatures:         make(map[string]*types.WorkloadSignature),
		reportsByType:      make(map[string]int64),
		reportsByNamespace: make(map[string]int64),
	}
}

// ReceiveReport processes an incoming insight report from a sidecar
func (c *InsightController) ReceiveReport(report *types.InsightReport) (*types.InsightReportResponse, error) {
	if report == nil {
		return nil, fmt.Errorf("report cannot be nil")
	}

	if report.PodName == "" || report.PodNamespace == "" {
		return nil, fmt.Errorf("pod_name and pod_namespace are required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Generate key for the workload
	key := fmt.Sprintf("%s/%s", report.PodNamespace, report.PodName)

	// Store or update the signature
	if report.Signature != nil {
		c.signatures[key] = report.Signature

		// Update metrics
		if report.Signature.WorkloadType != "" {
			c.reportsByType[report.Signature.WorkloadType]++
		}
		c.reportsByNamespace[report.PodNamespace]++
	}

	c.totalReports++
	c.lastReportTime = time.Now()

	// Log the report
	log.Printf("[Insight] Received report from %s (type=%s, io=%s, cpu=%.1f%%, mem=%.1f%%)",
		key,
		getStringOrDefault(report.Signature, "WorkloadType", "unknown"),
		getStringOrDefault(report.Signature, "IOPattern", "unknown"),
		getFloatOrDefault(report.Signature, "CPUUsagePercent", 0),
		getFloatOrDefault(report.Signature, "MemoryUsagePercent", 0))

	return &types.InsightReportResponse{
		Status:    "received",
		Message:   fmt.Sprintf("Report received for %s", key),
		RequestID: report.TraceID,
	}, nil
}

// GetSignature returns the current signature for a specific workload
func (c *InsightController) GetSignature(namespace, name string) (*types.WorkloadSignature, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := fmt.Sprintf("%s/%s", namespace, name)
	sig, exists := c.signatures[key]
	if !exists {
		return nil, fmt.Errorf("signature not found for %s", key)
	}

	return sig, nil
}

// ListSignatures returns all active workload signatures
func (c *InsightController) ListSignatures() []*types.WorkloadSignature {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*types.WorkloadSignature, 0, len(c.signatures))
	for _, sig := range c.signatures {
		result = append(result, sig)
	}

	return result
}

// GetMetrics returns insight metrics
func (c *InsightController) GetMetrics() *types.InsightMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Build workload summaries
	summaries := make([]types.WorkloadSummary, 0, len(c.signatures))
	for _, sig := range c.signatures {
		summaries = append(summaries, types.WorkloadSummary{
			PodName:      sig.PodName,
			PodNamespace: sig.PodNamespace,
			WorkloadType: sig.WorkloadType,
			IOPattern:    sig.IOPattern,
			LastUpdated:  sig.LastUpdated,
		})
	}

	// Copy maps to avoid race conditions
	reportsByType := make(map[string]int64)
	for k, v := range c.reportsByType {
		reportsByType[k] = v
	}
	reportsByNamespace := make(map[string]int64)
	for k, v := range c.reportsByNamespace {
		reportsByNamespace[k] = v
	}

	return &types.InsightMetrics{
		TotalReports:       c.totalReports,
		ActiveWorkloads:    len(c.signatures),
		ReportsByType:      reportsByType,
		ReportsByNamespace: reportsByNamespace,
		LastReportTime:     c.lastReportTime,
		WorkloadSummaries:  summaries,
	}
}

// CleanupStaleSignatures removes signatures that haven't been updated recently
func (c *InsightController) CleanupStaleSignatures(maxAge time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	for key, sig := range c.signatures {
		if sig.LastUpdated.Before(cutoff) {
			delete(c.signatures, key)
			removed++
			log.Printf("[Insight] Cleaned up stale signature for %s", key)
		}
	}

	return removed
}

// Helper functions
func getStringOrDefault(sig *types.WorkloadSignature, field string, defaultVal string) string {
	if sig == nil {
		return defaultVal
	}
	switch field {
	case "WorkloadType":
		if sig.WorkloadType != "" {
			return sig.WorkloadType
		}
	case "IOPattern":
		if sig.IOPattern != "" {
			return sig.IOPattern
		}
	}
	return defaultVal
}

func getFloatOrDefault(sig *types.WorkloadSignature, field string, defaultVal float64) float64 {
	if sig == nil {
		return defaultVal
	}
	switch field {
	case "CPUUsagePercent":
		return sig.CPUUsagePercent
	case "MemoryUsagePercent":
		return sig.MemoryUsagePercent
	}
	return defaultVal
}
