package controller

import (
	"strings"
	"sync"
	"time"

	"ai-storage-orchestrator/pkg/types"
)

// StageExecutionPlanStore는 run_id + target_stage 기준 계획을 관리한다.
type StageExecutionPlanStore struct {
	mu    sync.RWMutex
	plans map[string]*types.StageExecutionPlan
}

// NewStageExecutionPlanStore는 빈 계획 저장소를 생성한다.
func NewStageExecutionPlanStore() *StageExecutionPlanStore {
	return &StageExecutionPlanStore{
		plans: make(map[string]*types.StageExecutionPlan),
	}
}

// Save는 계획을 upsert 한다.
func (s *StageExecutionPlanStore) Save(plan *types.StageExecutionPlan) {
	if s == nil || plan == nil {
		return
	}
	now := time.Now()
	normalized := cloneStageExecutionPlan(plan)
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()

	key := stageExecutionPlanKey(normalized.RunID, normalized.TargetStage)
	if existing, ok := s.plans[key]; ok && !existing.CreatedAt.IsZero() {
		normalized.CreatedAt = existing.CreatedAt
	}
	s.plans[key] = normalized
}

// Get은 run_id + target_stage 기준 계획을 조회한다.
func (s *StageExecutionPlanStore) Get(runID, targetStage string) (*types.StageExecutionPlan, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	plan, ok := s.plans[stageExecutionPlanKey(runID, targetStage)]
	if !ok {
		return nil, false
	}
	return cloneStageExecutionPlan(plan), true
}

func stageExecutionPlanKey(runID, targetStage string) string {
	return strings.TrimSpace(runID) + "::" + strings.ToLower(strings.TrimSpace(targetStage))
}

func cloneStageExecutionPlan(plan *types.StageExecutionPlan) *types.StageExecutionPlan {
	if plan == nil {
		return nil
	}
	cloned := *plan
	cloned.ResourceRequests = cloneStringMap(plan.ResourceRequests)
	cloned.NodeSelector = cloneStringMap(plan.NodeSelector)
	cloned.StorageAnnotations = cloneStringMap(plan.StorageAnnotations)
	cloned.PolicyAnnotations = cloneStringMap(plan.PolicyAnnotations)
	cloned.Affinity = cloneAnyMap(plan.Affinity)
	return &cloned
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneAnyMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
