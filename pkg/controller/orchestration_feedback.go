package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type orchestrationFeedbackPayload struct {
	PolicyName      string                 `json:"policy_name"`
	TargetWorkload  string                 `json:"target_workload"`
	Namespace       string                 `json:"namespace"`
	Node            string                 `json:"node"`
	Action          string                 `json:"action"`
	Result          string                 `json:"result"`
	TimestampUnixMs int64                  `json:"timestamp_unix_ms"`
	BeforeState     map[string]interface{} `json:"before_state,omitempty"`
	AfterState      map[string]interface{} `json:"after_state,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

func (mc *MigrationController) publishOrchestrationResult(job *MigrationJob, result string, errMsg string) {
	if job == nil || job.Request == nil {
		return
	}
	endpoint := strings.TrimSpace(os.Getenv("INSIGHT_HUB_ORCH_RESULT_ENDPOINT"))
	if endpoint == "" {
		return
	}

	payload := orchestrationFeedbackPayload{
		PolicyName:      fallback(job.Request.PolicyName, "migration-default"),
		TargetWorkload:  job.Request.PodName,
		Namespace:       job.Request.PodNamespace,
		Node:            fallback(job.Request.TargetNode, "unknown"),
		Action:          fallback(job.Request.Action, "migrate"),
		Result:          result,
		TimestampUnixMs: time.Now().UnixMilli(),
		BeforeState:     mc.buildBeforeState(job),
		AfterState:      mc.buildAfterState(job),
		Metadata: map[string]interface{}{
			"migration_id":  job.ID,
			"source_node":   job.Request.SourceNode,
			"target_node":   job.Request.TargetNode,
			"checkpoint_pvc": safeDetailsValue(job, func() string { return job.Details.PVClaimName }),
			"error_message": errMsg,
			"stage":         safeDetailsValue(job, func() string { return job.Details.CurrentStage }),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("orchestration result marshal failed: migration=%s err=%v", job.ID, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		log.Printf("orchestration result request build failed: migration=%s err=%v", job.ID, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("orchestration result publish failed: migration=%s endpoint=%s err=%v", job.ID, endpoint, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		log.Printf("orchestration result publish rejected: migration=%s endpoint=%s status=%d", job.ID, endpoint, resp.StatusCode)
		return
	}
	log.Printf("orchestration result published: migration=%s policy=%s result=%s endpoint=%s", job.ID, payload.PolicyName, result, endpoint)
}

func (mc *MigrationController) buildBeforeState(job *MigrationJob) map[string]interface{} {
	before := map[string]interface{}{}
	if job != nil && job.Details != nil && job.Details.OriginalResources != nil {
		before["cpu_usage"] = job.Details.OriginalResources.CPUUsage
		before["memory_usage"] = job.Details.OriginalResources.MemoryUsage
		before["timestamp_unix_ms"] = job.Details.OriginalResources.Timestamp.UnixMilli()
	}
	if job != nil && job.Details != nil && len(job.Details.ContainerStates) > 0 {
		before["container_states"] = job.Details.ContainerStates
	}
	return before
}

func (mc *MigrationController) buildAfterState(job *MigrationJob) map[string]interface{} {
	after := map[string]interface{}{}
	if job != nil && job.Details != nil && job.Details.OptimizedResources != nil {
		after["cpu_usage"] = job.Details.OptimizedResources.CPUUsage
		after["memory_usage"] = job.Details.OptimizedResources.MemoryUsage
		after["timestamp_unix_ms"] = job.Details.OptimizedResources.Timestamp.UnixMilli()
	}
	if job != nil && job.Details != nil {
		after["new_pod_name"] = job.Details.NewPodName
		after["current_stage"] = job.Details.CurrentStage
		after["stage_history"] = job.Details.StageHistory
	}
	return after
}

func fallback(value string, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func safeDetailsValue(job *MigrationJob, getter func() string) string {
	if job == nil || job.Details == nil || getter == nil {
		return ""
	}
	return strings.TrimSpace(getter())
}

func (mc *MigrationController) formatResultLogLine(job *MigrationJob, result string) string {
	if job == nil || job.Request == nil {
		return "migration result unavailable"
	}
	return fmt.Sprintf("migration=%s workload=%s/%s action=%s result=%s", job.ID, job.Request.PodNamespace, job.Request.PodName, fallback(job.Request.Action, "migrate"), result)
}
