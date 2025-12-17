# DCGM Exporter Setup Guide

## Overview

This guide explains how to deploy and configure NVIDIA DCGM (Data Center GPU Manager) Exporter for real-time GPU metrics collection in the AI Storage Orchestrator autoscaling system.

## Prerequisites

- Kubernetes cluster with GPU nodes
- NVIDIA GPU Operator or Device Plugin installed
- GPU nodes labeled with `nvidia.com/gpu=present`
- kubectl access to the cluster

## Deployment Steps

### 1. Verify GPU Nodes

Check that your GPU nodes have the required label:

```bash
kubectl get nodes -l nvidia.com/gpu=present
```

If nodes are not labeled, add the label:

```bash
kubectl label nodes <gpu-node-name> nvidia.com/gpu=present
```

### 2. Deploy DCGM Exporter

Apply the DCGM Exporter DaemonSet:

```bash
kubectl apply -f deployments/dcgm-exporter.yaml
```

This creates:
- Namespace: `gpu-monitoring`
- DaemonSet: `dcgm-exporter` (runs on all GPU nodes)
- Service: `dcgm-exporter` (exposes metrics on port 9400)

### 3. Verify Deployment

Check that DCGM Exporter pods are running:

```bash
kubectl get pods -n gpu-monitoring
```

Expected output:
```
NAME                  READY   STATUS    RESTARTS   AGE
dcgm-exporter-xxxxx   1/1     Running   0          1m
```

### 4. Test Metrics Collection

Query DCGM metrics endpoint:

```bash
kubectl run test-curl --image=curlimages/curl:latest --rm -i --restart=Never -- \
  curl -s http://dcgm-exporter.gpu-monitoring.svc.cluster.local:9400/metrics
```

Look for metrics like:
- `DCGM_FI_DEV_GPU_UTIL` - GPU utilization percentage
- `DCGM_FI_DEV_MEM_COPY_UTIL` - Memory bandwidth utilization
- `DCGM_FI_DEV_FB_USED` - Frame buffer memory used

### 5. Verify Pod-Level Metrics

Check that DCGM is collecting metrics for specific pods:

```bash
kubectl run test-curl --image=curlimages/curl:latest --rm -i --restart=Never -- \
  curl -s http://dcgm-exporter.gpu-monitoring.svc.cluster.local:9400/metrics | \
  grep 'DCGM_FI_DEV_GPU_UTIL.*namespace="default"'
```

## Integration with Autoscaler

The AI Storage Orchestrator automatically queries DCGM Exporter for GPU metrics when:

1. A pod has GPU resource requests (`nvidia.com/gpu` or `amd.com/gpu`)
2. The pod is in `Running` status
3. DCGM Exporter service is accessible

### Metric Collection Flow

```
┌─────────────────┐
│  Autoscaler     │
│  Controller     │
└────────┬────────┘
         │
         │ 1. Detect GPU pod
         │
         ▼
┌─────────────────┐
│ calculatePod    │
│ GPUUtilization  │
└────────┬────────┘
         │
         │ 2. Query DCGM
         │
         ▼
┌─────────────────┐      HTTP GET      ┌──────────────┐
│ getGPUUtil      │ ─────────────────▶ │ DCGM         │
│ FromDCGM        │                    │ Exporter     │
└────────┬────────┘ ◀───────────────── │ Service      │
         │           Prometheus         └──────────────┘
         │           metrics
         │
         │ 3. Parse metrics
         │
         ▼
┌─────────────────┐
│ Filter by       │
│ namespace/pod   │
│ Calculate avg   │
└────────┬────────┘
         │
         │ 4. Return GPU %
         │
         ▼
┌─────────────────┐
│  Scaling        │
│  Decision       │
└─────────────────┘
```

### Fallback Behavior

If DCGM metrics are unavailable, the system falls back to simulated values:
- Base utilization: 60%
- Random variance: 0-30%
- Total range: 60-90%

This ensures autoscaling continues to function even if DCGM Exporter is temporarily unavailable.

## Troubleshooting

### DCGM Pod Not Starting

**Problem:** DCGM Exporter pod stuck in `Pending` or `CrashLoopBackOff`

**Solutions:**
1. Check node selector matches GPU nodes:
   ```bash
   kubectl describe pod -n gpu-monitoring <pod-name>
   ```

2. Verify NVIDIA device plugin is running:
   ```bash
   kubectl get pods -n kube-system -l name=nvidia-device-plugin-ds
   ```

3. Check DCGM pod logs:
   ```bash
   kubectl logs -n gpu-monitoring <pod-name>
   ```

### No GPU Metrics in Autoscaler

**Problem:** Autoscaler shows "Failed to get real metrics" for GPU pods

**Solutions:**
1. Verify DCGM service is accessible from orchestrator:
   ```bash
   kubectl exec -n kube-system <orchestrator-pod> -- \
     wget -O- http://dcgm-exporter.gpu-monitoring.svc.cluster.local:9400/metrics
   ```

2. Check if pod has GPU resource requests:
   ```bash
   kubectl get pod <pod-name> -o jsonpath='{.spec.containers[*].resources.requests}'
   ```

3. Verify pod-level metrics exist in DCGM:
   ```bash
   curl http://dcgm-exporter.gpu-monitoring:9400/metrics | \
     grep "DCGM_FI_DEV_GPU_UTIL.*pod=\"<pod-name>\""
   ```

### GPU Utilization Always 0%

**Problem:** DCGM reports 0% GPU utilization for running workloads

**Explanation:** This is expected if:
- The workload is not actively using GPU (e.g., just running `nvidia-smi`)
- The workload is between computation bursts
- GPU memory is allocated but not being computed on

**To test with real GPU load:**

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: gpu-burn-test
spec:
  containers:
  - name: gpu-burn
    image: nvidia/cuda:11.8.0-base-ubuntu22.04
    command: ["sh", "-c", "while true; do nvidia-smi --query-gpu=utilization.gpu --format=csv,noheader,nounits; sleep 1; done"]
    resources:
      limits:
        nvidia.com/gpu: 1
```

For actual GPU load testing, use tools like:
- `gpu-burn` - stress test
- TensorFlow/PyTorch training workloads
- CUDA sample programs

## Monitoring DCGM Metrics

### Available Metrics

DCGM Exporter provides extensive GPU metrics:

| Metric | Description |
|--------|-------------|
| `DCGM_FI_DEV_GPU_UTIL` | GPU utilization (%) |
| `DCGM_FI_DEV_MEM_COPY_UTIL` | Memory bandwidth utilization (%) |
| `DCGM_FI_DEV_FB_USED` | Frame buffer memory used (MB) |
| `DCGM_FI_DEV_FB_FREE` | Frame buffer memory free (MB) |
| `DCGM_FI_DEV_POWER_USAGE` | Power usage (W) |
| `DCGM_FI_DEV_GPU_TEMP` | GPU temperature (°C) |
| `DCGM_FI_DEV_SM_CLOCK` | SM clock frequency (MHz) |
| `DCGM_FI_DEV_MEM_CLOCK` | Memory clock frequency (MHz) |

### Integration with Prometheus (Optional)

If you have Prometheus installed, add DCGM Exporter as a scrape target:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: dcgm-exporter
  namespace: gpu-monitoring
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "9400"
```

Prometheus will automatically discover and scrape DCGM metrics.

## Performance Considerations

- **Collection Frequency:** DCGM collects metrics every 1-2 seconds by default
- **Overhead:** Minimal (<0.5% GPU utilization)
- **Network Traffic:** ~10-20 KB/s per GPU for metric export
- **Historical Data:** DCGM stores recent metrics in memory (configurable)

## Security

DCGM Exporter runs with:
- `privileged: true` - Required for GPU access
- `hostNetwork: true` - Required for accessing kubelet pod resources
- `SYS_ADMIN` capability - Required for DCGM operations

These permissions are necessary for DCGM to function properly in Kubernetes.

## Comparison: DCGM vs nvidia-smi

| Feature | DCGM Exporter | nvidia-smi exec |
|---------|---------------|-----------------|
| Collection method | Continuous monitoring | Snapshot on demand |
| Overhead | Very low | Higher (process exec) |
| Metrics granularity | 1-2 second intervals | Per-request only |
| Historical data | Yes (in-memory) | No |
| Prometheus format | Yes | No |
| Pod-level attribution | Yes | Manual parsing |
| Multi-GPU support | Excellent | Good |
| Requires nvidia-smi in pod | No | Yes |

## Related Documentation

- [Autoscaling API Guide](./autoscaling_api_guide.md)
- [DCGM Official Docs](https://docs.nvidia.com/datacenter/dcgm/latest/)
- [DCGM Exporter GitHub](https://github.com/NVIDIA/dcgm-exporter)
