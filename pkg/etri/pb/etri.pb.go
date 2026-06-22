package pb

// NOTE:
// 이 파일은 proto/etri.proto 를 기준으로 한 최소 수동 구현이며,
// 실제 환경에서는 protoc 로 생성된 코드를 사용하는 것이 권장된다.

import (
	context "context"

	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// Message types (필요 필드만 최소 구현)
// ---------------------------------------------------------------------------

type EtriWorkloadMetadata struct {
	WorkloadName string            `json:"workload_name,omitempty"`
	Namespace    string            `json:"namespace,omitempty"`
	JobType      string            `json:"job_type,omitempty"`
	Stage        string            `json:"stage,omitempty"`
	Framework    string            `json:"framework,omitempty"`
	PipelineId   string            `json:"pipeline_id,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Datasets     []*DatasetReference
	Execution    *ExecutionRequirement
	Scheduling   *SchedulingHint
	Orchestration *OrchestrationHint
}

type DatasetReference struct {
	DatasetName     string            `json:"dataset_name,omitempty"`
	DatasetId       string            `json:"dataset_id,omitempty"`
	LogicalPath     string            `json:"logical_path,omitempty"`
	AccessPattern   string            `json:"access_pattern,omitempty"`
	CachePreferred  bool              `json:"cache_preferred,omitempty"`
	LocalFirst      bool              `json:"local_first,omitempty"`
	ApproxSizeBytes int64             `json:"approx_size_bytes,omitempty"`
	FileCount       int64             `json:"file_count,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
}

type ExecutionRequirement struct {
	GpuCount          int32  `json:"gpu_count,omitempty"`
	Cpu               string `json:"cpu,omitempty"`
	Memory            string `json:"memory,omitempty"`
	Distributed       bool   `json:"distributed,omitempty"`
	PriorityClassName string `json:"priority_class_name,omitempty"`
	QosClass          string `json:"qos_class,omitempty"`
}

type SchedulingHint struct {
	LocalityPreference    string            `json:"locality_preference,omitempty"`
	PreferCsd             bool              `json:"prefer_csd,omitempty"`
	PreferGpu             bool              `json:"prefer_gpu,omitempty"`
	AdditionalLabels      map[string]string `json:"additional_labels,omitempty"`
	AdditionalAnnotations map[string]string `json:"additional_annotations,omitempty"`
}

type OrchestrationHint struct {
	EnableMigration   bool   `json:"enable_migration,omitempty"`
	EnableCheckpoint  bool   `json:"enable_checkpoint,omitempty"`
	EnableGlobalCache bool   `json:"enable_global_cache,omitempty"`
	Strategy          string `json:"strategy,omitempty"`
}

type RegisterWorkloadMetadataRequest struct {
	Metadata *EtriWorkloadMetadata `json:"metadata,omitempty"`
}

type RegisterWorkloadMetadataResponse struct {
	WorkloadName string `json:"workload_name,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
	Message      string `json:"message,omitempty"`
}

type WorkloadStatusRequest struct {
	WorkloadName string `json:"workload_name,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
}

func (w *WorkloadStatusRequest) GetWorkloadName() string { return w.WorkloadName }
func (w *WorkloadStatusRequest) GetNamespace() string    { return w.Namespace }

type WorkloadStatusResponse struct {
	WorkloadName                 string            `json:"workload_name,omitempty"`
	Namespace                    string            `json:"namespace,omitempty"`
	Phase                        string            `json:"phase,omitempty"`
	NodeName                     string            `json:"node_name,omitempty"`
	Locality                     string            `json:"locality,omitempty"`
	DatasetName                  string            `json:"dataset_name,omitempty"`
	Message                      string            `json:"message,omitempty"`
	LastTransitionTimeUnixSeconds int64             `json:"last_transition_time_unix_seconds,omitempty"`
	Labels                       map[string]string `json:"labels,omitempty"`
	Annotations                  map[string]string `json:"annotations,omitempty"`
}

type ReportWorkloadResultRequest struct {
	Status *WorkloadStatusResponse `json:"status,omitempty"`
}

type ReportWorkloadResultResponse struct {
	Message string `json:"message,omitempty"`
}

type GetDatasetBindingInfoRequest struct {
	DatasetName string `json:"dataset_name,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
}

func (r *GetDatasetBindingInfoRequest) GetDatasetName() string { return r.DatasetName }
func (r *GetDatasetBindingInfoRequest) GetNamespace() string   { return r.Namespace }

type GetDatasetBindingInfoResponse struct {
	DatasetName        string `json:"dataset_name,omitempty"`
	Namespace          string `json:"namespace,omitempty"`
	BindingDescription string `json:"binding_description,omitempty"`
}

// ---------------------------------------------------------------------------
// Service 정의 및 등록
// ---------------------------------------------------------------------------

type EtriIntegrationServiceServer interface {
	RegisterWorkloadMetadata(context.Context, *RegisterWorkloadMetadataRequest) (*RegisterWorkloadMetadataResponse, error)
	GetWorkloadStatus(context.Context, *WorkloadStatusRequest) (*WorkloadStatusResponse, error)
	ReportWorkloadResult(context.Context, *ReportWorkloadResultRequest) (*ReportWorkloadResultResponse, error)
	GetDatasetBindingInfo(context.Context, *GetDatasetBindingInfoRequest) (*GetDatasetBindingInfoResponse, error)
}

// UnimplementedEtriIntegrationServiceServer 는 forward compatibility 를 위한 기본 구현
type UnimplementedEtriIntegrationServiceServer struct{}

func (*UnimplementedEtriIntegrationServiceServer) RegisterWorkloadMetadata(context.Context, *RegisterWorkloadMetadataRequest) (*RegisterWorkloadMetadataResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RegisterWorkloadMetadata not implemented")
}
func (*UnimplementedEtriIntegrationServiceServer) GetWorkloadStatus(context.Context, *WorkloadStatusRequest) (*WorkloadStatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetWorkloadStatus not implemented")
}
func (*UnimplementedEtriIntegrationServiceServer) ReportWorkloadResult(context.Context, *ReportWorkloadResultRequest) (*ReportWorkloadResultResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ReportWorkloadResult not implemented")
}
func (*UnimplementedEtriIntegrationServiceServer) GetDatasetBindingInfo(context.Context, *GetDatasetBindingInfoRequest) (*GetDatasetBindingInfoResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetDatasetBindingInfo not implemented")
}

// Register 함수 (grpc.Server 에 서비스 등록)
func RegisterEtriIntegrationServiceServer(s *grpc.Server, srv EtriIntegrationServiceServer) {
	s.RegisterService(&_EtriIntegrationService_serviceDesc, srv)
}

var _EtriIntegrationService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "etri.v1.EtriIntegrationService",
	HandlerType: (*EtriIntegrationServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "RegisterWorkloadMetadata",
			Handler:    _EtriIntegrationService_RegisterWorkloadMetadata_Handler,
		},
		{
			MethodName: "GetWorkloadStatus",
			Handler:    _EtriIntegrationService_GetWorkloadStatus_Handler,
		},
		{
			MethodName: "ReportWorkloadResult",
			Handler:    _EtriIntegrationService_ReportWorkloadResult_Handler,
		},
		{
			MethodName: "GetDatasetBindingInfo",
			Handler:    _EtriIntegrationService_GetDatasetBindingInfo_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "proto/etri.proto",
}

func _EtriIntegrationService_RegisterWorkloadMetadata_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RegisterWorkloadMetadataRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EtriIntegrationServiceServer).RegisterWorkloadMetadata(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/etri.v1.EtriIntegrationService/RegisterWorkloadMetadata",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EtriIntegrationServiceServer).RegisterWorkloadMetadata(ctx, req.(*RegisterWorkloadMetadataRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _EtriIntegrationService_GetWorkloadStatus_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(WorkloadStatusRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EtriIntegrationServiceServer).GetWorkloadStatus(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/etri.v1.EtriIntegrationService/GetWorkloadStatus",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EtriIntegrationServiceServer).GetWorkloadStatus(ctx, req.(*WorkloadStatusRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _EtriIntegrationService_ReportWorkloadResult_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ReportWorkloadResultRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EtriIntegrationServiceServer).ReportWorkloadResult(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/etri.v1.EtriIntegrationService/ReportWorkloadResult",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EtriIntegrationServiceServer).ReportWorkloadResult(ctx, req.(*ReportWorkloadResultRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _EtriIntegrationService_GetDatasetBindingInfo_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetDatasetBindingInfoRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(EtriIntegrationServiceServer).GetDatasetBindingInfo(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/etri.v1.EtriIntegrationService/GetDatasetBindingInfo",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(EtriIntegrationServiceServer).GetDatasetBindingInfo(ctx, req.(*GetDatasetBindingInfoRequest))
	}
	return interceptor(ctx, in, info, handler)
}

