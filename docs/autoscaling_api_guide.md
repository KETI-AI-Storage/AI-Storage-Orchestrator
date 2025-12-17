# AI Storage Orchestrator - Autoscaling API Guide

## Overview

The AI Storage Orchestrator provides a sophisticated autoscaling system for Kubernetes workloads. This system monitors CPU, Memory, and GPU utilization to automatically scale workloads (Deployments, StatefulSets, ReplicaSets) based on configured thresholds.

**Key Features:**
- Multi-metric autoscaling (CPU, Memory, GPU)
- Stabilization windows to prevent flapping
- Configurable scale-up and scale-down policies
- Per-workload autoscaler management
- Real-time metrics tracking

## API Endpoints

### Base URL
```
http://<orchestrator-service>:8080/api/v1
```

### 1. Create Autoscaler

Creates a new autoscaler for a workload.

**Endpoint:** `POST /autoscaling`

**Request Body:**
```json
{
  "workload_name": "nginx-deployment",
  "workload_namespace": "default",
  "workload_type": "Deployment",
  "min_replicas": 2,
  "max_replicas": 10,
  "target_cpu_percent": 70,
  "target_memory_percent": 80,
  "target_gpu_percent": 75,
  "scale_up_policy": {
    "stabilization_window_seconds": 60,
    "max_scale_change": 3
  },
  "scale_down_policy": {
    "stabilization_window_seconds": 300,
    "max_scale_change": 2
  }
}
```

**Request Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `workload_name` | string | Yes | Name of the target workload |
| `workload_namespace` | string | Yes | Namespace of the target workload |
| `workload_type` | string | Yes | Type of workload: `Deployment`, `StatefulSet`, or `ReplicaSet` |
| `min_replicas` | int32 | Yes | Minimum number of replicas (must be >= 1) |
| `max_replicas` | int32 | Yes | Maximum number of replicas (must be >= min_replicas) |
| `target_cpu_percent` | int32 | No | Target CPU utilization percentage (0-100) |
| `target_memory_percent` | int32 | No | Target memory utilization percentage (0-100) |
| `target_gpu_percent` | int32 | No | Target GPU utilization percentage (0-100) |
| `scale_up_policy` | object | No | Scale-up behavior configuration |
| `scale_down_policy` | object | No | Scale-down behavior configuration |

**Scaling Policy Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `stabilization_window_seconds` | int32 | Time window to observe metrics before scaling (default: 0 for scale-up, 300 for scale-down) |
| `max_scale_change` | int32 | Maximum number of replicas to add/remove in a single scaling operation |

**Response (201 Created):**
```json
{
  "autoscaling_id": "autoscaler-a1b2c3d4",
  "status": "active",
  "message": "Autoscaler created successfully",
  "details": {
    "created_at": "2025-12-15T10:30:00Z",
    "current_replicas": 2,
    "desired_replicas": 2,
    "current_cpu_percent": 0,
    "current_memory_percent": 0,
    "current_gpu_percent": 0,
    "scale_up_count": 0,
    "scale_down_count": 0,
    "hpa_name": "hpa-nginx-deployment-abc123"
  }
}
```

**Example:**
```bash
curl -X POST http://localhost:8080/api/v1/autoscaling \
  -H "Content-Type: application/json" \
  -d '{
    "workload_name": "ai-training-job",
    "workload_namespace": "ml-workloads",
    "workload_type": "Deployment",
    "min_replicas": 1,
    "max_replicas": 5,
    "target_cpu_percent": 70,
    "target_gpu_percent": 80,
    "scale_up_policy": {
      "stabilization_window_seconds": 60,
      "max_scale_change": 2
    },
    "scale_down_policy": {
      "stabilization_window_seconds": 300,
      "max_scale_change": 1
    }
  }'
```

---

### 2. Get Autoscaler Status

Retrieves the current status of an autoscaler.

**Endpoint:** `GET /autoscaling/:id`

**Path Parameters:**
- `id` - Autoscaler ID (returned when creating the autoscaler)

**Response (200 OK):**
```json
{
  "autoscaling_id": "autoscaler-a1b2c3d4",
  "status": "active",
  "message": "Autoscaler is active",
  "details": {
    "created_at": "2025-12-15T10:30:00Z",
    "updated_at": "2025-12-15T10:35:00Z",
    "current_replicas": 3,
    "desired_replicas": 3,
    "current_cpu_percent": 75,
    "current_memory_percent": 60,
    "current_gpu_percent": 82,
    "last_scale_time": "2025-12-15T10:34:00Z",
    "scale_up_count": 2,
    "scale_down_count": 1,
    "hpa_name": "hpa-nginx-deployment-abc123"
  }
}
```

**Example:**
```bash
curl http://localhost:8080/api/v1/autoscaling/autoscaler-a1b2c3d4
```

---

### 3. List All Autoscalers

Lists all active autoscalers.

**Endpoint:** `GET /autoscaling`

**Response (200 OK):**
```json
{
  "autoscalers": [
    {
      "autoscaling_id": "autoscaler-a1b2c3d4",
      "status": "active",
      "message": "Autoscaler is active",
      "details": {
        "created_at": "2025-12-15T10:30:00Z",
        "current_replicas": 3,
        "desired_replicas": 3,
        "current_cpu_percent": 75,
        "scale_up_count": 2,
        "scale_down_count": 1
      }
    },
    {
      "autoscaling_id": "autoscaler-e5f6g7h8",
      "status": "active",
      "message": "Autoscaler is active",
      "details": {
        "created_at": "2025-12-15T11:00:00Z",
        "current_replicas": 2,
        "desired_replicas": 2,
        "current_cpu_percent": 45,
        "scale_up_count": 0,
        "scale_down_count": 0
      }
    }
  ],
  "count": 2
}
```

**Example:**
```bash
curl http://localhost:8080/api/v1/autoscaling
```

---

### 4. Delete Autoscaler

Stops and removes an autoscaler.

**Endpoint:** `DELETE /autoscaling/:id`

**Path Parameters:**
- `id` - Autoscaler ID

**Response (200 OK):**
```json
{
  "message": "Autoscaler deleted successfully",
  "autoscaler_id": "autoscaler-a1b2c3d4"
}
```

**Example:**
```bash
curl -X DELETE http://localhost:8080/api/v1/autoscaling/autoscaler-a1b2c3d4
```

---

### 5. Get Autoscaling Metrics

Retrieves aggregated metrics across all autoscalers.

**Endpoint:** `GET /autoscaling/metrics`

**Response (200 OK):**
```json
{
  "total_autoscalers": 5,
  "active_autoscalers": 4,
  "total_scale_ups": 23,
  "total_scale_downs": 12,
  "average_cpu_utilization": 68.5
}
```

**Example:**
```bash
curl http://localhost:8080/api/v1/autoscaling/metrics
```

---

## How Autoscaling Works

### Scaling Decision Process

1. **Metric Collection (every 15 seconds)**
   - Collects current replica count
   - Queries CPU, Memory, GPU utilization from Kubernetes Metrics API
   - Calculates average utilization across all running pods

2. **Desired Replica Calculation**
   - For each metric (CPU/Memory/GPU) with a target set:
     ```
     desired_replicas = current_replicas × (current_utilization / target_utilization)
     ```
   - Takes the maximum of all calculated values
   - Applies min/max replica constraints
   - Applies max_scale_change limits

3. **Stabilization Window**
   - Tracks scaling recommendations over time
   - **Scale-up**: Uses maximum recommendation within the window (aggressive)
   - **Scale-down**: Uses maximum recommendation within the window (conservative)
   - Default windows: 0s for scale-up, 300s (5 min) for scale-down

4. **Scaling Execution**
   - Updates workload replica count via Kubernetes API
   - Logs scaling events
   - Updates metrics counters

### Stabilization Window Behavior

The stabilization window prevents rapid scaling oscillations (flapping):

**Scale-Up Example:**
- Target CPU: 70%, Current: 85%
- System recommends scaling from 2 to 3 replicas
- With 60s stabilization window, waits for 60s of consistent high utilization
- Uses the maximum recommendation during that window

**Scale-Down Example:**
- Target CPU: 70%, Current: 40%
- System recommends scaling from 3 to 2 replicas
- With 300s stabilization window, waits 5 minutes
- Uses the maximum recommendation during that window (prevents premature scale-down)

**Why maximum for both directions?**
- Scale-up: Ensures responsiveness to load spikes
- Scale-down: Prevents aggressive scale-down during temporary load dips

---

## Configuration Examples

### Example 1: CPU-Only Autoscaling
```json
{
  "workload_name": "web-frontend",
  "workload_namespace": "production",
  "workload_type": "Deployment",
  "min_replicas": 2,
  "max_replicas": 10,
  "target_cpu_percent": 70
}
```

### Example 2: Multi-Metric Autoscaling with Policies
```json
{
  "workload_name": "ai-inference",
  "workload_namespace": "ml",
  "workload_type": "Deployment",
  "min_replicas": 1,
  "max_replicas": 8,
  "target_cpu_percent": 60,
  "target_memory_percent": 75,
  "target_gpu_percent": 80,
  "scale_up_policy": {
    "stabilization_window_seconds": 30,
    "max_scale_change": 3
  },
  "scale_down_policy": {
    "stabilization_window_seconds": 600,
    "max_scale_change": 1
  }
}
```

### Example 3: GPU-Only Autoscaling for Training
```json
{
  "workload_name": "model-training",
  "workload_namespace": "research",
  "workload_type": "StatefulSet",
  "min_replicas": 1,
  "max_replicas": 4,
  "target_gpu_percent": 85,
  "scale_down_policy": {
    "stabilization_window_seconds": 900,
    "max_scale_change": 1
  }
}
```

---

## Best Practices

### 1. Choose Appropriate Targets
- **CPU-bound workloads**: Set `target_cpu_percent` to 60-70%
- **Memory-bound workloads**: Set `target_memory_percent` to 70-80%
- **GPU workloads**: Set `target_gpu_percent` to 75-85%

### 2. Configure Stabilization Windows
- **Scale-up**: Short window (30-60s) for responsiveness
- **Scale-down**: Long window (300-600s) to prevent premature scale-down
- **Production workloads**: Use longer scale-down windows (600s+)

### 3. Set Realistic Min/Max Replicas
- **Min replicas**: Ensure high availability (at least 2 for production)
- **Max replicas**: Consider resource quotas and cluster capacity

### 4. Limit Scale Changes
- **scale_up_policy.max_scale_change**: 2-3 replicas to prevent resource spikes
- **scale_down_policy.max_scale_change**: 1-2 replicas for gradual scale-down

### 5. Monitor Autoscaler Behavior
- Check autoscaler logs regularly
- Monitor `scale_up_count` and `scale_down_count` metrics
- Adjust targets if excessive scaling events occur

---

## Troubleshooting

### Autoscaler Not Scaling

**Problem:** Workload stays at min_replicas despite high utilization

**Solutions:**
1. Check metrics availability:
   ```bash
   kubectl top pods -n <namespace>
   ```
2. Verify workload has resource requests defined
3. Check autoscaler logs:
   ```bash
   kubectl logs -n kube-system -l app=ai-storage-orchestrator
   ```
4. Ensure target metrics are set (at least one required)

### Excessive Scaling Events

**Problem:** Autoscaler scales up and down too frequently

**Solutions:**
1. Increase stabilization windows
2. Increase target utilization thresholds
3. Set `max_scale_change` to limit scaling rate
4. Check for metric fluctuations in application

### GPU Metrics Always Zero

**Problem:** `current_gpu_percent` is always 0

**Solution:** The system now uses NVIDIA DCGM Exporter for real-time GPU metrics collection. Ensure:
1. DCGM Exporter is deployed in the `gpu-monitoring` namespace
2. GPU nodes have the label `nvidia.com/gpu=present`
3. Pods requesting GPU resources are running

**Fallback Behavior:** If DCGM Exporter is not available or metrics cannot be retrieved, the system falls back to simulated utilization (60-90%) for pods with GPU requests.

---

## API Error Responses

### 400 Bad Request
```json
{
  "error": "Validation failed",
  "details": "min_replicas must be at least 1"
}
```

### 404 Not Found
```json
{
  "error": "Autoscaler not found",
  "details": "autoscaler autoscaler-xyz123 not found"
}
```

### 500 Internal Server Error
```json
{
  "error": "Failed to create autoscaler",
  "details": "failed to get workload: deployment \"nginx\" not found"
}
```

---

## Comparison with Kubernetes HPA

| Feature | AI Storage Autoscaler | Kubernetes HPA |
|---------|----------------------|----------------|
| Multi-metric support | CPU, Memory, GPU | CPU, Memory, Custom |
| GPU autoscaling | Built-in (simulated) | Requires custom metrics |
| Stabilization window | Configurable per policy | Fixed (default 5min down) |
| Max scale change | Configurable | No limit |
| Management API | RESTful HTTP | kubectl only |
| Metrics tracking | Built-in dashboard | Requires Prometheus |
| CSD awareness | Planned | No |

---

## Future Enhancements

- **CSD Resource Awareness**: Factor in Computational Storage Device metrics
- **Predictive Autoscaling**: ML-based workload prediction
- **Multi-cluster Support**: Autoscaling across multiple clusters
- **Custom Metrics**: Support for application-specific metrics via Prometheus
- **Webhooks**: Event notifications for scaling actions
- **GPU Memory Metrics**: Track GPU memory utilization in addition to compute utilization
- **Multi-GPU Pod Support**: Better handling of pods with multiple GPUs

---

## Related Documentation

- [Migration API Guide](./migration_controller_explanation_ko.md)
- [Deployment Guide](../deployments/README.md)
- [Testing Specification](./CERTIFICATION_TEST_SPEC.md)
