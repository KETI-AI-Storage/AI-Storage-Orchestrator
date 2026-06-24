package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TestGetPodContainerStates_ShouldMigrateDecision locks in the per-container migrate
// decision that drives the optimization: waiting/completed are dropped, running/failed
// are carried over. (completed exit-0 dropped == the resource saving.)
func TestGetPodContainerStates_ShouldMigrateDecision(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "waiting-c"}, {Name: "running-c"}, {Name: "completed-c"}, {Name: "failed-c"},
		}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "waiting-c", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"}}},
			{Name: "running-c", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			{Name: "completed-c", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
			{Name: "failed-c", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}},
		}},
	}

	states, err := (&Client{}).GetPodContainerStates(context.Background(), pod)
	if err != nil {
		t.Fatalf("GetPodContainerStates: %v", err)
	}

	wantMigrate := map[string]bool{"waiting-c": false, "running-c": true, "completed-c": false, "failed-c": true}
	wantState := map[string]string{"waiting-c": "waiting", "running-c": "running", "completed-c": "completed", "failed-c": "failed"}
	if len(states) != 4 {
		t.Fatalf("got %d states, want 4", len(states))
	}
	for _, s := range states {
		if s.ShouldMigrate != wantMigrate[s.Name] {
			t.Errorf("%s: ShouldMigrate=%v, want %v", s.Name, s.ShouldMigrate, wantMigrate[s.Name])
		}
		if s.State != wantState[s.Name] {
			t.Errorf("%s: State=%q, want %q", s.Name, s.State, wantState[s.Name])
		}
	}
}
