package types

// StorageClass 표준 (Gluesys CSI 기반)
const (
	StorageClassGluesysDatasetInput  = "gluesys-dataset-input"
	StorageClassGluesysDatasetOutput = "gluesys-dataset-output"
	StorageClassGluesysCache         = "gluesys-cache"
)

// 표준 mountPath
const (
	MountPathInput  = "/data/input"
	MountPathOutput = "/data/output"
	MountPathCache  = "/data/cache"
)

// PVC 역할
type PVCRole string

const (
	PVCRoleDatasetInput  PVCRole = "dataset-input"
	PVCRoleDatasetOutput PVCRole = "dataset-output"
	PVCRoleCache         PVCRole = "cache"
)

// 워크로드 타입 (스토리지 정책 기준)
type WorkloadType string

const (
	WorkloadTypePreprocessing WorkloadType = "preprocessing"
	WorkloadTypeTraining      WorkloadType = "training"
	WorkloadTypeInference     WorkloadType = "inference"
	WorkloadTypeCacheWarmup   WorkloadType = "cache-warmup"
)

// PVC 하나에 대한 스토리지 요구사항
type StorageRequirement struct {
	Role         PVCRole     // dataset-input / dataset-output / cache
	StorageClass string      // gluesys-dataset-input / gluesys-dataset-output / gluesys-cache
	MountPath    string      // /data/input / /data/output / /data/cache
	WorkloadType WorkloadType
}

// BuildPVCName 는 워크로드 이름과 역할에 따라 사람이 이해하기 쉬운 PVC 이름을 만든다.
// 예: training-job + dataset-input  -> training-job-input
//     training-job + dataset-output -> training-job-output
//     training-job + cache          -> training-job-cache
func BuildPVCName(workloadName string, role PVCRole) string {
	switch role {
	case PVCRoleDatasetInput:
		return workloadName + "-input"
	case PVCRoleDatasetOutput:
		return workloadName + "-output"
	case PVCRoleCache:
		return workloadName + "-cache"
	default:
		return workloadName + "-pvc"
	}
}

// GetStoragePolicy 는 workload 타입과 옵션을 기준으로
// 어떤 PVC 역할/StorageClass/mountPath 구성이 필요한지 반환한다.
//
// needOutput: inference 처럼 기본은 input만 있지만 output도 필요할 때 true
// needCache : cache-warmup 외에 cache가 추가로 필요할 때 true
func GetStoragePolicy(workloadType WorkloadType, needOutput, needCache bool) []StorageRequirement {
	reqs := make([]StorageRequirement, 0, 3)

	switch workloadType {
	case WorkloadTypePreprocessing:
		// preprocessing: dataset-input + dataset-output
		reqs = append(reqs,
			StorageRequirement{
				Role:         PVCRoleDatasetInput,
				StorageClass: StorageClassGluesysDatasetInput,
				MountPath:    MountPathInput,
				WorkloadType: workloadType,
			},
			StorageRequirement{
				Role:         PVCRoleDatasetOutput,
				StorageClass: StorageClassGluesysDatasetOutput,
				MountPath:    MountPathOutput,
				WorkloadType: workloadType,
			},
		)

	case WorkloadTypeTraining:
		// training: dataset-input + dataset-output
		reqs = append(reqs,
			StorageRequirement{
				Role:         PVCRoleDatasetInput,
				StorageClass: StorageClassGluesysDatasetInput,
				MountPath:    MountPathInput,
				WorkloadType: workloadType,
			},
			StorageRequirement{
				Role:         PVCRoleDatasetOutput,
				StorageClass: StorageClassGluesysDatasetOutput,
				MountPath:    MountPathOutput,
				WorkloadType: workloadType,
			},
		)

	case WorkloadTypeInference:
		// inference: 기본은 dataset-input, 필요 시 dataset-output
		reqs = append(reqs, StorageRequirement{
			Role:         PVCRoleDatasetInput,
			StorageClass: StorageClassGluesysDatasetInput,
			MountPath:    MountPathInput,
			WorkloadType: workloadType,
		})
		if needOutput {
			reqs = append(reqs, StorageRequirement{
				Role:         PVCRoleDatasetOutput,
				StorageClass: StorageClassGluesysDatasetOutput,
				MountPath:    MountPathOutput,
				WorkloadType: workloadType,
			})
		}

	case WorkloadTypeCacheWarmup:
		// cache-warmup: dataset-input + cache
		reqs = append(reqs,
			StorageRequirement{
				Role:         PVCRoleDatasetInput,
				StorageClass: StorageClassGluesysDatasetInput,
				MountPath:    MountPathInput,
				WorkloadType: workloadType,
			},
			StorageRequirement{
				Role:         PVCRoleCache,
				StorageClass: StorageClassGluesysCache,
				MountPath:    MountPathCache,
				WorkloadType: workloadType,
			},
		)

	default:
		// 알 수 없는 타입은 보수적으로 input만 붙인다.
		reqs = append(reqs, StorageRequirement{
			Role:         PVCRoleDatasetInput,
			StorageClass: StorageClassGluesysDatasetInput,
			MountPath:    MountPathInput,
			WorkloadType: workloadType,
		})
		if needOutput {
			reqs = append(reqs, StorageRequirement{
				Role:         PVCRoleDatasetOutput,
				StorageClass: StorageClassGluesysDatasetOutput,
				MountPath:    MountPathOutput,
				WorkloadType: workloadType,
			})
		}
		if needCache {
			reqs = append(reqs, StorageRequirement{
				Role:         PVCRoleCache,
				StorageClass: StorageClassGluesysCache,
				MountPath:    MountPathCache,
				WorkloadType: workloadType,
			})
		}
	}

	// 옵션 플래그로 cache 추가
	if needCache {
		hasCache := false
		for _, r := range reqs {
			if r.Role == PVCRoleCache {
				hasCache = true
				break
			}
		}
		if !hasCache {
			reqs = append(reqs, StorageRequirement{
				Role:         PVCRoleCache,
				StorageClass: StorageClassGluesysCache,
				MountPath:    MountPathCache,
				WorkloadType: workloadType,
			})
		}
	}

	return reqs
}

