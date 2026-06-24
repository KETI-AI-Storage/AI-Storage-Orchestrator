package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"ai-storage-orchestrator/pkg/types"
)

func optVolCount(pod *corev1.Pod, name string) int {
	n := 0
	for _, v := range pod.Spec.Volumes {
		if v.Name == name {
			n++
		}
	}
	return n
}

func optContainerNames(pod *corev1.Pod) []string {
	out := make([]string, 0, len(pod.Spec.Containers))
	for _, c := range pod.Spec.Containers {
		out = append(out, c.Name)
	}
	return out
}

// TestBuildOptimizedPodSpec_ExcludesCompletedContainers locks in the core optimization
// behind the project's 50%/40% claim: only ShouldMigrate containers are carried over,
// so completed containers (and their resource requests) are dropped on the target node.
func TestBuildOptimizedPodSpec_ExcludesCompletedContainers(t *testing.T) {
	original := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "ai-storage-workloads"},
		Spec: corev1.PodSpec{
			NodeName: "gpu-server-03",
			Containers: []corev1.Container{
				{Name: "completed-step", Image: "busybox"},
				{Name: "running-step", Image: "busybox"},
			},
		},
	}
	states := []types.ContainerState{
		{Name: "completed-step", State: "completed", ShouldMigrate: false},
		{Name: "running-step", State: "running", ShouldMigrate: true},
	}

	opt := buildOptimizedPodSpec(original, "csd-server-01", states, "checkpoint-demo", "/data")

	if got := optContainerNames(opt); len(got) != 1 || got[0] != "running-step" {
		t.Fatalf("optimized containers = %v, want [running-step] (completed-step excluded)", got)
	}
	if opt.Spec.NodeName != "csd-server-01" {
		t.Errorf("NodeName = %q, want csd-server-01", opt.Spec.NodeName)
	}
	if opt.Labels["migration.ai-storage/job"] != "true" || opt.Labels["migration.ai-storage/original-pod"] != "demo" {
		t.Errorf("migration labels not set correctly: %v", opt.Labels)
	}
	if n := optVolCount(opt, "checkpoint-volume"); n != 1 {
		t.Errorf("checkpoint-volume count = %d, want 1", n)
	}
}

// TestBuildOptimizedPodSpec_NoDuplicateCheckpointVolumeOnReMigration locks in the
// re-migration safety: migrating a pod that ALREADY carries a checkpoint-volume (i.e.
// it is itself a prior migration product) must NOT yield a duplicate volume, which the
// API server rejects with `spec.volumes[N].name: Duplicate value: "checkpoint-volume"`.
// The surviving volume must reference the fresh checkpoint PVC, not the stale inherited one.
func TestBuildOptimizedPodSpec_NoDuplicateCheckpointVolumeOnReMigration(t *testing.T) {
	original := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-migrated-aaa", Namespace: "ai-storage-workloads"},
		Spec: corev1.PodSpec{
			NodeName: "gpu-server-03",
			Containers: []corev1.Container{{
				Name:  "running-step",
				Image: "busybox",
				VolumeMounts: []corev1.VolumeMount{
					{Name: "checkpoint-volume", MountPath: "/migration-checkpoint"},
				},
			}},
			Volumes: []corev1.Volume{{
				Name: "checkpoint-volume",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "stale-checkpoint"},
				},
			}},
		},
	}
	states := []types.ContainerState{{Name: "running-step", State: "running", ShouldMigrate: true}}

	opt := buildOptimizedPodSpec(original, "csd-server-01", states, "fresh-checkpoint", "/data")

	if n := optVolCount(opt, "checkpoint-volume"); n != 1 {
		t.Fatalf("checkpoint-volume count = %d, want exactly 1 (re-migration must not duplicate)", n)
	}
	for _, v := range opt.Spec.Volumes {
		if v.Name == "checkpoint-volume" {
			if v.PersistentVolumeClaim == nil || v.PersistentVolumeClaim.ClaimName != "fresh-checkpoint" {
				t.Errorf("checkpoint-volume claim = %+v, want ClaimName=fresh-checkpoint", v.PersistentVolumeClaim)
			}
		}
	}
}
