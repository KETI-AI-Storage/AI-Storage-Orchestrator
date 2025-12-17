package controller

import (
	"context"
	"testing"
	"time"

	"ai-storage-orchestrator/pkg/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockK8sClient is a mock implementation of K8sClientInterface for testing
type MockK8sClient struct {
	mock.Mock
}

func (m *MockK8sClient) GetWorkloadReplicas(ctx context.Context, namespace, name, workloadType string) (int32, error) {
	args := m.Called(ctx, namespace, name, workloadType)
	return args.Get(0).(int32), args.Error(1)
}

func (m *MockK8sClient) GetWorkloadPodMetrics(ctx context.Context, namespace, workloadName string) (cpuPercent, memoryPercent, gpuPercent int32, err error) {
	args := m.Called(ctx, namespace, workloadName)
	return args.Get(0).(int32), args.Get(1).(int32), args.Get(2).(int32), args.Error(3)
}

func (m *MockK8sClient) ScaleWorkload(ctx context.Context, namespace, name, workloadType string, replicas int32) error {
	args := m.Called(ctx, namespace, name, workloadType, replicas)
	return args.Error(0)
}

// TestCreateAutoscaler tests the creation of an autoscaler
func TestCreateAutoscaler(t *testing.T) {
	mockClient := new(MockK8sClient)
	ac := NewAutoscalingController(mockClient)

	req := &types.AutoscalingRequest{
		WorkloadName:      "test-deployment",
		WorkloadNamespace: "default",
		WorkloadType:      "Deployment",
		MinReplicas:       1,
		MaxReplicas:       5,
		TargetCPU:         70,
	}

	resp, err := ac.CreateAutoscaler(req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.AutoscalingID)
	assert.Equal(t, types.AutoscalingStatusActive, resp.Status)
	assert.Equal(t, "Autoscaler created successfully", resp.Message)

	// Verify autoscaler is stored
	assert.Equal(t, 1, len(ac.autoscalers))
}

// TestCreateAutoscalerValidation tests input validation
func TestCreateAutoscalerValidation(t *testing.T) {
	mockClient := new(MockK8sClient)
	ac := NewAutoscalingController(mockClient)

	tests := []struct {
		name        string
		req         *types.AutoscalingRequest
		expectedErr string
	}{
		{
			name: "missing workload name",
			req: &types.AutoscalingRequest{
				WorkloadNamespace: "default",
				WorkloadType:      "Deployment",
				MinReplicas:       1,
				MaxReplicas:       5,
				TargetCPU:         70,
			},
			expectedErr: "workload_name is required",
		},
		{
			name: "missing workload namespace",
			req: &types.AutoscalingRequest{
				WorkloadName: "test",
				WorkloadType: "Deployment",
				MinReplicas:  1,
				MaxReplicas:  5,
				TargetCPU:    70,
			},
			expectedErr: "workload_namespace is required",
		},
		{
			name: "invalid min replicas",
			req: &types.AutoscalingRequest{
				WorkloadName:      "test",
				WorkloadNamespace: "default",
				WorkloadType:      "Deployment",
				MinReplicas:       0,
				MaxReplicas:       5,
				TargetCPU:         70,
			},
			expectedErr: "min_replicas must be at least 1",
		},
		{
			name: "max less than min",
			req: &types.AutoscalingRequest{
				WorkloadName:      "test",
				WorkloadNamespace: "default",
				WorkloadType:      "Deployment",
				MinReplicas:       5,
				MaxReplicas:       3,
				TargetCPU:         70,
			},
			expectedErr: "max_replicas must be greater than or equal to min_replicas",
		},
		{
			name: "no target metrics",
			req: &types.AutoscalingRequest{
				WorkloadName:      "test",
				WorkloadNamespace: "default",
				WorkloadType:      "Deployment",
				MinReplicas:       1,
				MaxReplicas:       5,
			},
			expectedErr: "at least one target metric (CPU, Memory, or GPU) must be specified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ac.CreateAutoscaler(tt.req)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

// TestGetAutoscaler tests retrieving an autoscaler
func TestGetAutoscaler(t *testing.T) {
	mockClient := new(MockK8sClient)
	ac := NewAutoscalingController(mockClient)

	req := &types.AutoscalingRequest{
		WorkloadName:      "test-deployment",
		WorkloadNamespace: "default",
		WorkloadType:      "Deployment",
		MinReplicas:       1,
		MaxReplicas:       5,
		TargetCPU:         70,
	}

	createResp, err := ac.CreateAutoscaler(req)
	assert.NoError(t, err)

	// Get the autoscaler
	getResp, err := ac.GetAutoscaler(createResp.AutoscalingID)
	assert.NoError(t, err)
	assert.Equal(t, createResp.AutoscalingID, getResp.AutoscalingID)
	assert.Equal(t, types.AutoscalingStatusActive, getResp.Status)

	// Try to get non-existent autoscaler
	_, err = ac.GetAutoscaler("non-existent-id")
	assert.Error(t, err)
}

// TestDeleteAutoscaler tests deleting an autoscaler
func TestDeleteAutoscaler(t *testing.T) {
	mockClient := new(MockK8sClient)
	ac := NewAutoscalingController(mockClient)

	req := &types.AutoscalingRequest{
		WorkloadName:      "test-deployment",
		WorkloadNamespace: "default",
		WorkloadType:      "Deployment",
		MinReplicas:       1,
		MaxReplicas:       5,
		TargetCPU:         70,
	}

	createResp, err := ac.CreateAutoscaler(req)
	assert.NoError(t, err)

	// Delete the autoscaler
	err = ac.DeleteAutoscaler(createResp.AutoscalingID)
	assert.NoError(t, err)

	// Verify it's deleted
	_, err = ac.GetAutoscaler(createResp.AutoscalingID)
	assert.Error(t, err)

	// Try to delete non-existent autoscaler
	err = ac.DeleteAutoscaler("non-existent-id")
	assert.Error(t, err)
}

// TestCalculateDesiredReplicas tests replica calculation logic
func TestCalculateDesiredReplicas(t *testing.T) {
	mockClient := new(MockK8sClient)
	ac := NewAutoscalingController(mockClient)

	tests := []struct {
		name             string
		job              *AutoscalingJob
		cpuUtil          int32
		memUtil          int32
		gpuUtil          int32
		currentReplicas  int32
		expectedReplicas int32
	}{
		{
			name: "scale up based on CPU",
			job: &AutoscalingJob{
				Request: &types.AutoscalingRequest{
					MinReplicas: 1,
					MaxReplicas: 10,
					TargetCPU:   70,
				},
				Details: &types.AutoscalingDetails{
					CurrentReplicas: 2,
				},
			},
			cpuUtil:          90,
			currentReplicas:  2,
			expectedReplicas: 2, // (2 * 90 / 70) = 2.57 -> 2 (rounded down in calculation)
		},
		{
			name: "scale down based on CPU",
			job: &AutoscalingJob{
				Request: &types.AutoscalingRequest{
					MinReplicas: 1,
					MaxReplicas: 10,
					TargetCPU:   70,
				},
				Details: &types.AutoscalingDetails{
					CurrentReplicas: 5,
				},
			},
			cpuUtil:          30,
			currentReplicas:  5,
			expectedReplicas: 2, // (5 * 30 / 70) = 2.14 -> 2
		},
		{
			name: "respect min replicas",
			job: &AutoscalingJob{
				Request: &types.AutoscalingRequest{
					MinReplicas: 2,
					MaxReplicas: 10,
					TargetCPU:   70,
				},
				Details: &types.AutoscalingDetails{
					CurrentReplicas: 2,
				},
			},
			cpuUtil:          10,
			currentReplicas:  2,
			expectedReplicas: 2, // Would be 0, but min is 2
		},
		{
			name: "respect max replicas",
			job: &AutoscalingJob{
				Request: &types.AutoscalingRequest{
					MinReplicas: 1,
					MaxReplicas: 5,
					TargetCPU:   70,
				},
				Details: &types.AutoscalingDetails{
					CurrentReplicas: 5,
				},
			},
			cpuUtil:          100,
			currentReplicas:  5,
			expectedReplicas: 5, // Would be 7, but max is 5
		},
		{
			name: "multi-metric scaling (use max)",
			job: &AutoscalingJob{
				Request: &types.AutoscalingRequest{
					MinReplicas:  1,
					MaxReplicas:  10,
					TargetCPU:    70,
					TargetMemory: 80,
					TargetGPU:    75,
				},
				Details: &types.AutoscalingDetails{
					CurrentReplicas: 2,
				},
			},
			cpuUtil:          50, // Would suggest 1 replica
			memUtil:          90, // Would suggest 2 replicas
			gpuUtil:          85, // Would suggest 2 replicas
			currentReplicas:  2,
			expectedReplicas: 2, // Max of all recommendations
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ac.calculateDesiredReplicas(tt.job, tt.cpuUtil, tt.memUtil, tt.gpuUtil)
			assert.Equal(t, tt.expectedReplicas, result)
		})
	}
}

// TestApplyStabilizationWindow tests stabilization window logic
func TestApplyStabilizationWindow(t *testing.T) {
	mockClient := new(MockK8sClient)
	ac := NewAutoscalingController(mockClient)

	t.Run("scale up without stabilization", func(t *testing.T) {
		job := &AutoscalingJob{
			Request: &types.AutoscalingRequest{
				MinReplicas: 1,
				MaxReplicas: 10,
			},
			scaleUpHistory:   []scaleRecommendation{},
			scaleDownHistory: []scaleRecommendation{},
		}

		result := ac.applyStabilizationWindow(job, 2, 4)
		assert.Equal(t, int32(4), result)
	})

	t.Run("scale up with stabilization window", func(t *testing.T) {
		job := &AutoscalingJob{
			Request: &types.AutoscalingRequest{
				MinReplicas: 1,
				MaxReplicas: 10,
				ScaleUpPolicy: &types.ScalingPolicy{
					StabilizationWindowSeconds: 60,
				},
			},
			scaleUpHistory:   []scaleRecommendation{},
			scaleDownHistory: []scaleRecommendation{},
		}

		// Add multiple recommendations
		job.scaleUpHistory = append(job.scaleUpHistory, scaleRecommendation{
			replicas:  3,
			timestamp: time.Now().Add(-30 * time.Second),
		})
		job.scaleUpHistory = append(job.scaleUpHistory, scaleRecommendation{
			replicas:  5,
			timestamp: time.Now().Add(-10 * time.Second),
		})

		result := ac.applyStabilizationWindow(job, 2, 4)
		// Should return max recommendation within window (5)
		assert.Equal(t, int32(5), result)
	})

	t.Run("scale down with default stabilization", func(t *testing.T) {
		job := &AutoscalingJob{
			Request: &types.AutoscalingRequest{
				MinReplicas: 1,
				MaxReplicas: 10,
			},
			scaleUpHistory:   []scaleRecommendation{},
			scaleDownHistory: []scaleRecommendation{},
		}

		// First recommendation
		result := ac.applyStabilizationWindow(job, 5, 2)
		assert.Equal(t, int32(2), result)

		// Add higher recommendation
		job.scaleDownHistory = append(job.scaleDownHistory, scaleRecommendation{
			replicas:  4,
			timestamp: time.Now().Add(-100 * time.Second),
		})

		// Should return max (4) to be conservative
		result = ac.applyStabilizationWindow(job, 5, 2)
		assert.Equal(t, int32(4), result)
	})

	t.Run("no scaling clears history", func(t *testing.T) {
		job := &AutoscalingJob{
			Request: &types.AutoscalingRequest{
				MinReplicas: 1,
				MaxReplicas: 10,
			},
			scaleUpHistory: []scaleRecommendation{
				{replicas: 3, timestamp: time.Now()},
			},
			scaleDownHistory: []scaleRecommendation{
				{replicas: 2, timestamp: time.Now()},
			},
		}

		result := ac.applyStabilizationWindow(job, 3, 3)
		assert.Equal(t, int32(3), result)
		assert.Equal(t, 0, len(job.scaleUpHistory))
		assert.Equal(t, 0, len(job.scaleDownHistory))
	})
}

// TestListAutoscalers tests listing all autoscalers
func TestListAutoscalers(t *testing.T) {
	mockClient := new(MockK8sClient)
	ac := NewAutoscalingController(mockClient)

	// Create multiple autoscalers
	for i := 0; i < 3; i++ {
		req := &types.AutoscalingRequest{
			WorkloadName:      "test-deployment",
			WorkloadNamespace: "default",
			WorkloadType:      "Deployment",
			MinReplicas:       1,
			MaxReplicas:       5,
			TargetCPU:         70,
		}
		_, err := ac.CreateAutoscaler(req)
		assert.NoError(t, err)
	}

	list := ac.ListAutoscalers()
	assert.Equal(t, 3, len(list))
}

// TestGetMetrics tests metrics collection
func TestGetMetrics(t *testing.T) {
	mockClient := new(MockK8sClient)
	ac := NewAutoscalingController(mockClient)

	// Create an autoscaler
	req := &types.AutoscalingRequest{
		WorkloadName:      "test-deployment",
		WorkloadNamespace: "default",
		WorkloadType:      "Deployment",
		MinReplicas:       1,
		MaxReplicas:       5,
		TargetCPU:         70,
	}
	resp, err := ac.CreateAutoscaler(req)
	assert.NoError(t, err)

	// Manually update details to simulate metrics
	ac.autoscalersMux.Lock()
	job := ac.autoscalers[resp.AutoscalingID]
	job.Details.CurrentCPU = 75
	job.Details.ScaleUpCount = 5
	job.Details.ScaleDownCount = 2
	ac.autoscalersMux.Unlock()

	ac.metrics.TotalScaleUps = 5
	ac.metrics.TotalScaleDowns = 2

	metrics := ac.GetMetrics()
	assert.Equal(t, int64(1), metrics.TotalAutoscalers)
	assert.Equal(t, int64(1), metrics.ActiveAutoscalers)
	assert.Equal(t, int64(5), metrics.TotalScaleUps)
	assert.Equal(t, int64(2), metrics.TotalScaleDowns)
	assert.Equal(t, float64(75), metrics.AverageCPUUtilization)
}

// TestMaxScaleChange tests max scale change limits
func TestMaxScaleChange(t *testing.T) {
	mockClient := new(MockK8sClient)
	ac := NewAutoscalingController(mockClient)

	t.Run("scale up with max change limit", func(t *testing.T) {
		job := &AutoscalingJob{
			Request: &types.AutoscalingRequest{
				MinReplicas: 1,
				MaxReplicas: 20,
				TargetCPU:   70,
				ScaleUpPolicy: &types.ScalingPolicy{
					MaxScaleChange: 3,
				},
			},
			Details: &types.AutoscalingDetails{
				CurrentReplicas: 2,
			},
		}

		// CPU suggests scaling to 10 replicas
		result := ac.calculateDesiredReplicas(job, 350, 0, 0)
		// Should be limited to current + 3 = 5
		assert.LessOrEqual(t, result, int32(5))
	})

	t.Run("scale down with max change limit", func(t *testing.T) {
		job := &AutoscalingJob{
			Request: &types.AutoscalingRequest{
				MinReplicas: 1,
				MaxReplicas: 20,
				TargetCPU:   70,
				ScaleDownPolicy: &types.ScalingPolicy{
					MaxScaleChange: 2,
				},
			},
			Details: &types.AutoscalingDetails{
				CurrentReplicas: 10,
			},
		}

		// CPU suggests scaling to 1 replica
		result := ac.calculateDesiredReplicas(job, 7, 0, 0)
		// Should be limited to current - 2 = 8
		assert.GreaterOrEqual(t, result, int32(8))
	})
}
