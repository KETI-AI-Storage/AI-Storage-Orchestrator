package etri

import (
	"context"
	"fmt"
	"time"

	"ai-storage-orchestrator/pkg/controller"
)

// Service exposes use cases for ETRI ↔ KETI integration.
type Service struct {
	repo     Repository
	k8sClient controller.K8sClientInterface
}

// NewService creates a new ETRI integration service.
func NewService(repo Repository, k8sClient controller.K8sClientInterface) *Service {
	return &Service{
		repo:     repo,
		k8sClient: k8sClient,
	}
}

// RegisterWorkloadMetadata stores metadata from ETRI and optionally precomputes hints.
func (s *Service) RegisterWorkloadMetadata(ctx context.Context, meta *EtriWorkloadMetadata) error {
	if meta == nil {
		return fmt.Errorf("metadata is nil")
	}
	if meta.WorkloadName == "" || meta.Namespace == "" {
		return fmt.Errorf("workload_name and namespace are required")
	}
	// Save raw metadata
	if err := s.repo.SaveWorkloadMetadata(ctx, meta); err != nil {
		return err
	}
	// Optionally, translate to labels/annotations for internal use.
	_ = s.TranslateEtriMetadataToInternalHints(meta)
	return nil
}

// GetWorkloadStatus returns current workload status, partially derived from K8s when possible.
func (s *Service) GetWorkloadStatus(ctx context.Context, ref WorkloadRef) (*WorkloadStatusResponse, error) {
	// First, check repository (may have explicit status reported earlier).
	if st, err := s.repo.GetWorkloadStatus(ctx, ref); err != nil {
		return nil, err
	} else if st != nil {
		return st, nil
	}

	// Fallback: derive status in a best-effort way using K8sClientInterface metrics or pod info.
	// NOTE: K8sClientInterface currently does not expose direct pod status,
	// so this part is intentionally minimal and will be enhanced in a future iteration.

	now := time.Now()
	status := &WorkloadStatusResponse{
		WorkloadName:       ref.WorkloadName,
		Namespace:          ref.Namespace,
		Phase:              WorkloadPhaseUnknown,
		Locality:           "",
		DatasetName:        "",
		Message:            "status derived from stub; no explicit workload status recorded yet",
		LastTransitionTime: &now,
	}

	// If we have metadata, enrich labels/annotations in the response.
	meta, err := s.repo.GetWorkloadMetadata(ctx, ref)
	if err == nil && meta != nil {
		status.Labels, status.Annotations = s.BuildK8sLabelsOrAnnotationsFromMetadata(meta)
		if len(meta.Datasets) > 0 {
			status.DatasetName = meta.Datasets[0].DatasetName
		}
	}

	return status, nil
}

// UpdateWorkloadExecutionResult allows ETRI or internal components to push a final result.
func (s *Service) UpdateWorkloadExecutionResult(ctx context.Context, status *WorkloadStatusResponse) error {
	if status == nil {
		return fmt.Errorf("status is nil")
	}
	if status.WorkloadName == "" || status.Namespace == "" {
		return fmt.Errorf("workload_name and namespace are required")
	}
	now := time.Now()
	if status.LastTransitionTime == nil {
		status.LastTransitionTime = &now
	}
	return s.repo.SaveWorkloadStatus(ctx, status)
}

// TranslateEtriMetadataToInternalHints converts ETRI metadata into internal labels/annotations.
// This is intentionally conservative and can be extended when real policies are defined.
func (s *Service) TranslateEtriMetadataToInternalHints(meta *EtriWorkloadMetadata) map[string]string {
	labels := map[string]string{}

	// Basic identity
	if meta.Stage != "" {
		labels["etri.ai-storage/stage"] = meta.Stage
	}
	if meta.Framework != "" {
		labels["etri.ai-storage/framework"] = meta.Framework
	}
	if meta.JobType != "" {
		labels["etri.ai-storage/job-type"] = meta.JobType
	}

	// Locality preference
	if meta.Scheduling.LocalityPreference != "" {
		labels["etri.ai-storage/locality"] = meta.Scheduling.LocalityPreference
	}

	// Execution requirements
	if meta.Execution.Distributed {
		labels["etri.ai-storage/distributed"] = "true"
	}
	if meta.Execution.PriorityClassName != "" {
		labels["etri.ai-storage/priority-class"] = meta.Execution.PriorityClassName
	}

	// Merge user-provided labels (namespaced to avoid collisions if needed).
	for k, v := range meta.Labels {
		labels["etri.label/"+k] = v
	}

	return labels
}

// BuildK8sLabelsOrAnnotationsFromMetadata returns concrete labels/annotations ready to attach to K8s objects.
func (s *Service) BuildK8sLabelsOrAnnotationsFromMetadata(meta *EtriWorkloadMetadata) (map[string]string, map[string]string) {
	labels := s.TranslateEtriMetadataToInternalHints(meta)
	annotations := map[string]string{}

	// Dataset hints (only first dataset for now; can be extended).
	if len(meta.Datasets) > 0 {
		ds := meta.Datasets[0]
		if ds.DatasetID != "" {
			annotations["etri.ai-storage/dataset-id"] = ds.DatasetID
		}
		if ds.LogicalPath != "" {
			annotations["etri.ai-storage/logical-path"] = ds.LogicalPath
		}
		if ds.AccessPattern != "" {
			annotations["etri.ai-storage/access-pattern"] = ds.AccessPattern
		}
		if ds.CachePreferred {
			annotations["etri.ai-storage/cache-preferred"] = "true"
		}
		if ds.LocalFirst {
			annotations["etri.ai-storage/local-first"] = "true"
		}
	}

	// Orchestration hints
	if meta.Orchestration.EnableMigration {
		annotations["etri.ai-storage/enable-migration"] = "true"
	}
	if meta.Orchestration.EnableCheckpoint {
		annotations["etri.ai-storage/enable-checkpoint"] = "true"
	}
	if meta.Orchestration.EnableGlobalCache {
		annotations["etri.ai-storage/enable-global-cache"] = "true"
	}
	if meta.Orchestration.Strategy != "" {
		annotations["etri.ai-storage/orchestration-strategy"] = meta.Orchestration.Strategy
	}

	// Merge user-provided annotations
	for k, v := range meta.Annotations {
		annotations["etri.annotation/"+k] = v
	}

	return labels, annotations
}

