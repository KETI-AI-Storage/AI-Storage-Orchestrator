package k8s

import (
	"testing"

	"ai-storage-orchestrator/pkg/types"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// countVolumes returns how many volumes in the spec carry the given name.
func countVolumes(pod *corev1.Pod, name string) int {
	n := 0
	for _, v := range pod.Spec.Volumes {
		if v.Name == name {
			n++
		}
	}
	return n
}

func containerByName(pod *corev1.Pod, name string) *corev1.Container {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == name {
			return &pod.Spec.Containers[i]
		}
	}
	return nil
}

func countMounts(c *corev1.Container, name string) int {
	n := 0
	for _, m := range c.VolumeMounts {
		if m.Name == name {
			n++
		}
	}
	return n
}

// TestBuildOptimizedPodSpec_ReMigrationNoDuplicateCheckpointVolume reproduces
// the production failure observed by the E2E harness: re-migrating a pod that is
// itself a previous migration product (it already carries a "checkpoint-volume")
// produced a second volume of the same name, making the pod invalid:
//
//	spec.volumes[2].name: Duplicate value: "checkpoint-volume"
//
// The optimized spec must attach exactly ONE checkpoint volume, pointing at the
// fresh PVC for THIS migration.
func TestBuildOptimizedPodSpec_ReMigrationNoDuplicateCheckpointVolume(t *testing.T) {
	const (
		oldPVC = "checkpoint-already-migrated-1111"
		newPVC = "checkpoint-oe2e-target-2222"
		mount  = "/migration-checkpoint"
	)

	// A pod that was already migrated once: it inherits a stale checkpoint-volume
	// (and the running container already mounts it).
	original := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "workload-migrated-x9tq6", Namespace: "ai-storage-workloads"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "checkpoint-volume",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: oldPVC},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:         "running-step",
					Image:        "busybox:1.36",
					VolumeMounts: []corev1.VolumeMount{{Name: "checkpoint-volume", MountPath: mount}},
				},
				{Name: "completed-step", Image: "busybox:1.36"},
			},
		},
	}

	states := []types.ContainerState{
		{Name: "running-step", State: "running", ShouldMigrate: true},
		{Name: "completed-step", State: "completed", ShouldMigrate: false},
	}

	got := buildOptimizedPodSpec(original, "target-node", states, newPVC, mount)

	// (1) exactly one checkpoint-volume at the spec level.
	if n := countVolumes(got, "checkpoint-volume"); n != 1 {
		t.Fatalf("checkpoint-volume count = %d, want 1 (duplicate makes the pod invalid)", n)
	}

	// (2) that single volume points at the NEW PVC, not the stale one.
	for _, v := range got.Spec.Volumes {
		if v.Name == "checkpoint-volume" {
			if v.PersistentVolumeClaim == nil || v.PersistentVolumeClaim.ClaimName != newPVC {
				t.Fatalf("checkpoint-volume claim = %v, want %q", v.PersistentVolumeClaim, newPVC)
			}
		}
	}

	// (3) the carried-over container mounts checkpoint-volume exactly once.
	rc := containerByName(got, "running-step")
	if rc == nil {
		t.Fatal("running-step container missing from optimized pod")
	}
	if n := countMounts(rc, "checkpoint-volume"); n != 1 {
		t.Fatalf("running-step checkpoint-volume mounts = %d, want 1", n)
	}

	// (sanity) completed-step excluded by the optimization.
	if containerByName(got, "completed-step") != nil {
		t.Fatal("completed-step should be excluded from the optimized pod")
	}
}

// TestBuildOptimizedPodSpec_FirstMigrationAttachesCheckpointVolume guards the
// common case: a pod with no prior checkpoint volume still gets exactly one
// attached (the stale-volume strip must be a no-op here).
func TestBuildOptimizedPodSpec_FirstMigrationAttachesCheckpointVolume(t *testing.T) {
	original := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "fresh-workload", Namespace: "ai-storage-workloads"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "running-step", Image: "busybox:1.36"},
			},
		},
	}
	states := []types.ContainerState{{Name: "running-step", State: "running", ShouldMigrate: true}}

	got := buildOptimizedPodSpec(original, "target-node", states, "checkpoint-fresh-1234", "/migration-checkpoint")

	if n := countVolumes(got, "checkpoint-volume"); n != 1 {
		t.Fatalf("checkpoint-volume count = %d, want 1", n)
	}
	rc := containerByName(got, "running-step")
	if rc == nil {
		t.Fatal("running-step missing")
	}
	if n := countMounts(rc, "checkpoint-volume"); n != 1 {
		t.Fatalf("running-step checkpoint-volume mounts = %d, want 1", n)
	}
}
