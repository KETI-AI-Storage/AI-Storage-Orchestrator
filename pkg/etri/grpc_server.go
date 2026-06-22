package etri

import (
	"context"
	"time"

	pb "ai-storage-orchestrator/pkg/etri/pb"
)

// GRPCServer implements the gRPC EtriIntegrationServiceServer interface
// on top of the ETRI Service.
type GRPCServer struct {
	pb.UnimplementedEtriIntegrationServiceServer
	svc *Service
}

// NewGRPCServer creates a new gRPC server wrapper.
func NewGRPCServer(svc *Service) *GRPCServer {
	return &GRPCServer{svc: svc}
}

// RegisterWorkloadMetadata forwards the call to the Service layer.
func (s *GRPCServer) RegisterWorkloadMetadata(ctx context.Context, req *pb.RegisterWorkloadMetadataRequest) (*pb.RegisterWorkloadMetadataResponse, error) {
	if req == nil || req.Metadata == nil {
		return &pb.RegisterWorkloadMetadataResponse{
			Message: "metadata is nil",
		}, nil
	}

	meta := &EtriWorkloadMetadata{
		WorkloadName: req.Metadata.WorkloadName,
		Namespace:    req.Metadata.Namespace,
		JobType:      req.Metadata.JobType,
		Stage:        req.Metadata.Stage,
		Framework:    req.Metadata.Framework,
		PipelineID:   req.Metadata.PipelineId,
		Labels:       req.Metadata.Labels,
		Annotations:  req.Metadata.Annotations,
	}

	for _, ds := range req.Metadata.Datasets {
		meta.Datasets = append(meta.Datasets, DatasetReference{
			DatasetName:     ds.DatasetName,
			DatasetID:       ds.DatasetId,
			LogicalPath:     ds.LogicalPath,
			AccessPattern:   ds.AccessPattern,
			CachePreferred:  ds.CachePreferred,
			LocalFirst:      ds.LocalFirst,
			ApproxSizeBytes: ds.ApproxSizeBytes,
			FileCount:       ds.FileCount,
			Labels:          ds.Labels,
			Annotations:     ds.Annotations,
		})
	}

	meta.Execution = ExecutionRequirement{
		GPUCount:          req.Metadata.Execution.GpuCount,
		CPU:               req.Metadata.Execution.Cpu,
		Memory:            req.Metadata.Execution.Memory,
		Distributed:       req.Metadata.Execution.Distributed,
		PriorityClassName: req.Metadata.Execution.PriorityClassName,
		QoSClass:          req.Metadata.Execution.QosClass,
	}

	meta.Scheduling = SchedulingHint{
		LocalityPreference:    req.Metadata.Scheduling.LocalityPreference,
		PreferCSD:             req.Metadata.Scheduling.PreferCsd,
		PreferGPU:             req.Metadata.Scheduling.PreferGpu,
		AdditionalLabels:      req.Metadata.Scheduling.AdditionalLabels,
		AdditionalAnnotations: req.Metadata.Scheduling.AdditionalAnnotations,
	}

	meta.Orchestration = OrchestrationHint{
		EnableMigration:   req.Metadata.Orchestration.EnableMigration,
		EnableCheckpoint:  req.Metadata.Orchestration.EnableCheckpoint,
		EnableGlobalCache: req.Metadata.Orchestration.EnableGlobalCache,
		Strategy:          req.Metadata.Orchestration.Strategy,
	}

	if err := s.svc.RegisterWorkloadMetadata(ctx, meta); err != nil {
		return &pb.RegisterWorkloadMetadataResponse{
			WorkloadName: req.Metadata.WorkloadName,
			Namespace:    req.Metadata.Namespace,
			Message:      "failed to register workload metadata: " + err.Error(),
		}, nil
	}

	return &pb.RegisterWorkloadMetadataResponse{
		WorkloadName: req.Metadata.WorkloadName,
		Namespace:    req.Metadata.Namespace,
		Message:      "workload metadata registered",
	}, nil
}

// GetWorkloadStatus forwards the call to the Service layer.
func (s *GRPCServer) GetWorkloadStatus(ctx context.Context, req *pb.WorkloadStatusRequest) (*pb.WorkloadStatusResponse, error) {
	status, err := s.svc.GetWorkloadStatus(ctx, WorkloadRef{
		WorkloadName: req.GetWorkloadName(),
		Namespace:    req.GetNamespace(),
	})
	if err != nil || status == nil {
		// For this phase, return an empty response with message only.
		return &pb.WorkloadStatusResponse{
			WorkloadName: req.GetWorkloadName(),
			Namespace:    req.GetNamespace(),
			Phase:        string(WorkloadPhaseUnknown),
			Message:      "failed to get workload status",
		}, nil
	}

	var lastTransition int64
	if status.LastTransitionTime != nil {
		lastTransition = status.LastTransitionTime.Unix()
	} else {
		lastTransition = time.Now().Unix()
	}

	return &pb.WorkloadStatusResponse{
		WorkloadName:                status.WorkloadName,
		Namespace:                   status.Namespace,
		Phase:                       string(status.Phase),
		NodeName:                    status.NodeName,
		Locality:                    status.Locality,
		DatasetName:                 status.DatasetName,
		Message:                     status.Message,
		LastTransitionTimeUnixSeconds: lastTransition,
		Labels:                      status.Labels,
		Annotations:                 status.Annotations,
	}, nil
}

// ReportWorkloadResult updates status via the Service layer (stub but functional).
func (s *GRPCServer) ReportWorkloadResult(ctx context.Context, req *pb.ReportWorkloadResultRequest) (*pb.ReportWorkloadResultResponse, error) {
	if req == nil || req.Status == nil {
		return &pb.ReportWorkloadResultResponse{
			Message: "status is nil",
		}, nil
	}

	var ts *time.Time
	if req.Status.LastTransitionTimeUnixSeconds != 0 {
		t := time.Unix(req.Status.LastTransitionTimeUnixSeconds, 0)
		ts = &t
	}

	status := &WorkloadStatusResponse{
		WorkloadName:       req.Status.WorkloadName,
		Namespace:          req.Status.Namespace,
		Phase:              WorkloadStatusPhase(req.Status.Phase),
		NodeName:           req.Status.NodeName,
		Locality:           req.Status.Locality,
		DatasetName:        req.Status.DatasetName,
		Message:            req.Status.Message,
		LastTransitionTime: ts,
		Labels:             req.Status.Labels,
		Annotations:        req.Status.Annotations,
	}

	if err := s.svc.UpdateWorkloadExecutionResult(ctx, status); err != nil {
		return &pb.ReportWorkloadResultResponse{
			Message: "failed to update workload result: " + err.Error(),
		}, nil
	}

	return &pb.ReportWorkloadResultResponse{
		Message: "workload result updated",
	}, nil
}

// GetDatasetBindingInfo is implemented as a stub that returns a generic description.
func (s *GRPCServer) GetDatasetBindingInfo(ctx context.Context, req *pb.GetDatasetBindingInfoRequest) (*pb.GetDatasetBindingInfoResponse, error) {
	// In this phase we do not expose real PV/PVC or CSI details; instead we
	// provide a high-level, human-readable description only.
	desc := "binding information is not yet integrated with PVC/PV; " +
		"dataset will be resolved internally by KETI based on logical name and namespace"

	return &pb.GetDatasetBindingInfoResponse{
		DatasetName:       req.GetDatasetName(),
		Namespace:         req.GetNamespace(),
		BindingDescription: desc,
	}, nil
}


