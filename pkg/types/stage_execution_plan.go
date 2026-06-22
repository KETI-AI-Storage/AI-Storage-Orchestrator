// Package types는 StageExecutionPlan 조회 응답 타입을 정의한다.
//
// Author: 미정 <unknown>
// Created: 2026-06-16
package types

import "time"

// StageExecutionPlan은 run_id와 target_stage 기준으로 조회되는 주입 계획이다.
type StageExecutionPlan struct {
	RunID string `json:"run_id"`

	TargetStage string `json:"target_stage"`
	Kind        string `json:"kind"`

	ResourceRequests   map[string]string      `json:"resource_requests,omitempty"`
	SchedulerName      string                 `json:"scheduler_name,omitempty"`
	NodeSelector       map[string]string      `json:"node_selector,omitempty"`
	Affinity           map[string]interface{} `json:"affinity,omitempty"`
	QueueLabel         string                 `json:"queue_label,omitempty"`
	StorageAnnotations map[string]string      `json:"storage_annotations,omitempty"`
	PolicyAnnotations  map[string]string      `json:"policy_annotations,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
