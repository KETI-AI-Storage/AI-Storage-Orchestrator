package k8s

import (
	"log"

	"ai-storage-orchestrator/pkg/types"

	corev1 "k8s.io/api/core/v1"
)

// InjectStandardVolumes 는 Gluesys 스토리지 표준에 따라
// 주어진 Pod 스펙에 volumes / volumeMounts 를 주입한다.
// 실제 Admission Webhook 서버는 아직 없지만, 이 함수는 그에 준하는
// mutation 로직으로 재사용 가능하도록 분리했다.
//
// 정책:
// - mountPath 는 항상 /data/input, /data/output, /data/cache 로 강제
// - 사용자가 다른 mountPath 를 지정한 경우, 정책 우선으로 /data/* 로 덮어쓰고
//   로그에 충돌 사실을 남긴다 (거부하지 않고 정책 우선).
func InjectStandardVolumes(pod *corev1.Pod, workloadType types.WorkloadType, needOutput, needCache bool) {
	if pod == nil {
		return
	}

	policy := types.GetStoragePolicy(workloadType, needOutput, needCache)
	if len(policy) == 0 {
		return
	}

	log.Println("==================================================")
	log.Println("[KETI] Mutation")
	log.Println("==================================================")
	log.Printf("scheduler_name: %s", pod.Spec.SchedulerName)
	log.Printf("trace_enabled : %s", pod.Annotations["keti.insight/trace_enabled"])
	log.Printf("sidecar       : %s", pod.Annotations["keti.insight/sidecar"])

	// 기존 volumes / mounts 를 기준으로 중복 여부 확인
	volumeExists := func(name string) bool {
		for _, v := range pod.Spec.Volumes {
			if v.Name == name {
				return true
			}
		}
		return false
	}

	// mountPath 충돌 감지용
	hasMountPath := func(mounts []corev1.VolumeMount, path string) bool {
		for _, m := range mounts {
			if m.MountPath == path {
				return true
			}
		}
		return false
	}

	for _, req := range policy {
		// PVC 이름은 StoragePolicy 의 규칙과 맞춘다.
		pvcName := types.BuildPVCName(pod.Name, req.Role)
		volName := string(req.Role) + "-volume"

		// Volume 주입
		if !volumeExists(volName) {
			pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
				Name: volName,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcName,
					},
				},
			})
			log.Printf("storage_class : %s", req.StorageClass)
			log.Printf("pvc_name      : %s", pvcName)
			log.Printf("mount_path    : %s", req.MountPath)
			log.Printf("binded_volume : %s", string(req.Role))
		}

		// 각 컨테이너에 mountPath 주입 (표준 경로 강제)
		for i, c := range pod.Spec.Containers {
			// mountPath 이미 존재하면서 다른 volume 을 사용 중일 수 있다.
			if hasMountPath(c.VolumeMounts, req.MountPath) {
				log.Printf("binded_volume : %s", string(req.Role))
				log.Printf("mount_path    : %s", req.MountPath)
				continue
			}

			pod.Spec.Containers[i].VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      volName,
				MountPath: req.MountPath,
			})
			log.Printf("binded_volume : %s", string(req.Role))
			log.Printf("mount_path    : %s", req.MountPath)
		}
	}
}

