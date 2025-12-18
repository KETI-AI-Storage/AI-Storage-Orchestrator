# AI Storage Orchestrator - Autoscaling API Guide

## Overview

The AI Storage Orchestrator provides a sophisticated autoscaling system for Kubernetes workloads, specifically designed for AI/ML workloads. Unlike Kubernetes HPA which only supports CPU and Memory, this system monitors **CPU, Memory, GPU, and Storage I/O metrics** to automatically scale workloads (Deployments, StatefulSets, ReplicaSets) based on configured thresholds.

**Key Features:**
- **Multi-metric autoscaling** (CPU, Memory, GPU, Storage I/O)
- **Storage-aware scaling** for data-intensive AI/ML training workloads
- **Storage I/O metrics**: Read/Write throughput (MB/s), IOPS
- Stabilization windows to prevent flapping
- Configurable scale-up and scale-down policies
- Per-workload autoscaler management
- Real-time metrics tracking via Prometheus

**Why Storage I/O Matters for AI/ML:**
During AI model training, data loading from storage often becomes the bottleneck, not just GPU compute. This autoscaler considers storage read/write throughput and IOPS alongside traditional metrics, ensuring workloads scale when storage I/O becomes saturated.

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
  "target_storage_read_throughput_mbps": 500,
  "target_storage_write_throughput_mbps": 200,
  "target_storage_iops": 3000,
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
| `target_cpu_percent` | int32 | No* | Target CPU utilization percentage (0-100) |
| `target_memory_percent` | int32 | No* | Target memory utilization percentage (0-100) |
| `target_gpu_percent` | int32 | No* | Target GPU utilization percentage (0-100) |
| `target_storage_read_throughput_mbps` | int64 | No* | Target storage read throughput in MB/s |
| `target_storage_write_throughput_mbps` | int64 | No* | Target storage write throughput in MB/s |
| `target_storage_iops` | int64 | No* | Target storage I/O operations per second |
| `scale_up_policy` | object | No | Scale-up behavior configuration |
| `scale_down_policy` | object | No | Scale-down behavior configuration |

**Note:** At least one target metric (CPU, Memory, GPU, or Storage I/O) must be specified.

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
    "current_storage_read_throughput_mbps": 0,
    "current_storage_write_throughput_mbps": 0,
    "current_storage_iops": 0,
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
   - **Queries Storage I/O metrics** from Prometheus (node-exporter/cAdvisor):
     - Storage read throughput (MB/s): `rate(container_fs_reads_bytes_total[1m])`
     - Storage write throughput (MB/s): `rate(container_fs_writes_bytes_total[1m])`
     - Storage IOPS: `rate(container_fs_reads_total[1m]) + rate(container_fs_writes_total[1m])`
   - Falls back to PVC size estimation or simulation if Prometheus unavailable
   - Calculates average utilization across all running pods

2. **Desired Replica Calculation**
   - For each metric (CPU/Memory/GPU/Storage I/O) with a target set:
     ```
     desired_replicas = current_replicas × (current_metric / target_metric)
     ```
   - Takes the **maximum** of all calculated values (conservative approach)
   - Applies min/max replica constraints
   - Applies max_scale_change limits

   **Example:** If current_replicas=2, target_storage_read=500 MB/s, current_storage_read=800 MB/s:
   ```
   desired_replicas = 2 × (800 / 500) = 3.2 → 4 replicas (rounded up)
   ```

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

### Example 4: Storage-Aware AI Training (Recommended for Data-Intensive Workloads)
```json
{
  "workload_name": "imagenet-training",
  "workload_namespace": "ml-training",
  "workload_type": "Deployment",
  "min_replicas": 2,
  "max_replicas": 8,
  "target_gpu_percent": 80,
  "target_storage_read_throughput_mbps": 600,
  "target_storage_write_throughput_mbps": 150,
  "scale_up_policy": {
    "stabilization_window_seconds": 30,
    "max_scale_change": 2
  },
  "scale_down_policy": {
    "stabilization_window_seconds": 300,
    "max_scale_change": 1
  }
}
```

**Explanation:** This configuration scales based on both GPU utilization and storage I/O. If storage read throughput exceeds 600 MB/s (data loading bottleneck), the autoscaler will add replicas even if GPU is not saturated. This is critical for AI/ML training where dataset streaming from storage often becomes the bottleneck.

### Example 5: Storage-Only Autoscaling for Data Pipeline
```json
{
  "workload_name": "data-preprocessing",
  "workload_namespace": "etl",
  "workload_type": "Deployment",
  "min_replicas": 1,
  "max_replicas": 10,
  "target_storage_read_throughput_mbps": 800,
  "target_storage_write_throughput_mbps": 300,
  "target_storage_iops": 5000,
  "scale_up_policy": {
    "stabilization_window_seconds": 60,
    "max_scale_change": 3
  }
}
```

**Explanation:** For workloads that are purely I/O-bound (data transformation, ETL pipelines), storage metrics alone can drive autoscaling decisions.

---

## Best Practices

### 1. Choose Appropriate Targets
- **CPU-bound workloads**: Set `target_cpu_percent` to 60-70%
- **Memory-bound workloads**: Set `target_memory_percent` to 70-80%
- **GPU workloads**: Set `target_gpu_percent` to 75-85%
- **Storage-intensive AI/ML workloads**:
  - **Read throughput**: 400-800 MB/s (dataset streaming during training)
  - **Write throughput**: 100-300 MB/s (checkpoint saving)
  - **IOPS**: 2000-5000 (mixed read/write operations)
  - **Recommendation**: Always set storage read throughput for data-intensive training

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

### Storage I/O Metrics Not Collected

**Problem:** `current_storage_read_throughput_mbps`, `current_storage_write_throughput_mbps`, `current_storage_iops` are 0 or not accurate

**Solution:** Storage I/O metrics are collected via 3-tier approach:

**Priority 1: Prometheus (Recommended)**
1. Ensure Prometheus is deployed with node-exporter or cAdvisor:
   ```bash
   kubectl get pods -n monitoring | grep prometheus
   kubectl get pods -n monitoring | grep node-exporter
   ```
2. Verify Prometheus is accessible at:
   ```
   http://prometheus-server.monitoring.svc.cluster.local:9090
   ```
3. Test metric availability:
   ```bash
   curl "http://prometheus-server.monitoring:9090/api/v1/query?query=container_fs_reads_bytes_total"
   ```

**Priority 2: PVC Size Estimation**
- If Prometheus unavailable, the system estimates based on PVC size
- Larger PVCs get higher estimated throughput
- Formula: 100GB ≈ 200 MB/s read, 1TB ≈ 500 MB/s read

**Priority 3: AI/ML Workload Simulation**
- For pods with PVCs but no real metrics, simulates typical values:
  - Read: 200-800 MB/s
  - Write: 50-200 MB/s
  - IOPS: 1000-5000

**Note:** Real metrics are strongly recommended for production workloads. Deploy Prometheus with node-exporter for accurate storage I/O tracking.

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
| Multi-metric support | CPU, Memory, GPU, **Storage I/O** | CPU, Memory, Custom |
| GPU autoscaling | Built-in via DCGM Exporter | Requires custom metrics |
| **Storage I/O autoscaling** | **Built-in (Read/Write/IOPS)** | **Not available** |
| AI/ML workload optimization | Yes (storage-aware) | No |
| Stabilization window | Configurable per policy | Fixed (default 5min down) |
| Max scale change | Configurable | No limit |
| Management API | RESTful HTTP | kubectl only |
| Metrics tracking | Built-in dashboard | Requires Prometheus |
| Metrics source | Prometheus + K8s Metrics | K8s Metrics only |
| CSD awareness | Planned | No |

**Key Differentiator:** The AI Storage Autoscaler is the only system that considers **storage I/O bottlenecks** (read/write throughput, IOPS) for autoscaling decisions, making it ideal for data-intensive AI/ML training workloads where storage bandwidth often becomes the limiting factor before GPU saturation.

---

## Future Enhancements

- **CSD Resource Awareness**: Factor in Computational Storage Device metrics for intelligent data placement
- **Predictive Autoscaling**: ML-based workload prediction using historical patterns
- **Multi-cluster Support**: Autoscaling across multiple clusters
- **Custom Metrics**: Support for application-specific metrics via Prometheus
- **Webhooks**: Event notifications for scaling actions
- **GPU Memory Metrics**: Track GPU memory utilization in addition to compute utilization
- **Multi-GPU Pod Support**: Better handling of pods with multiple GPUs
- **Storage QoS Integration**: Coordinate with storage QoS policies for optimal performance
- **Network I/O Metrics**: Add network bandwidth monitoring for distributed training
- **Historical Trend Analysis**: Long-term storage I/O pattern analysis for better capacity planning

---

## Related Documentation

- [Migration API Guide](./migration_controller_explanation_ko.md)
- [Deployment Guide](../deployments/README.md)
- [Testing Specification](./CERTIFICATION_TEST_SPEC.md)
