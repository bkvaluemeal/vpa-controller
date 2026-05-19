VPA Controller
==============

The **VPA Controller** is a Kubernetes controller that manages
[Vertical Pod Autoscaler (VPA)][1] objects by dynamically updating their
resource policies based on external metrics, such as those from Prometheus.

While the standard VPA recommender uses historical resource usage, this
controller allows you to define custom logic for `minAllowed` and `maxAllowed`
resource boundaries using Prometheus queries.

## Features

- **Metric-Driven Boundaries**: Set VPA `minAllowed` and `maxAllowed` values
  using any Prometheus query
- **High Availability**: Built-in leader election support for running multiple
  replicas
- **Fine-Grained Control**: Configure settings at the container and resource
  level (CPU/Memory) via annotations
- **Optimistic Concurrency**: Safely manages VPA objects with last-sync tracking

---

## Example

To have the controller manage a VPA, add the `vpa.prometheus.io/schedule`
annotation and at least one query annotation.

```yaml
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: app
  annotations:
    # Sync every 5 minutes
    vpa.prometheus.io/schedule: 5m
    # CPU Minimum boundary for 'web' container
    vpa.prometheus.io/web-query-cpu-min: |
      avg(rate(container_cpu_usage_seconds_total{container='web'}[1h])) * 0.8
    # Memory Maximum boundary for 'web' container
    vpa.prometheus.io/web/-query-memory-max: |
      max(container_memory_working_set_bytes{container='web'}) * 1.5
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: app
```

---

## Annotation Reference

The controller uses the following annotation format:

| Annotation | Description | Example |
|------------|-------------|---------|
| `{{PREFIX}}/schedule` | How often to reconcile the VPA. | `5m`, `1h` |
| `{{PREFIX}}/{{CONTAINER}}-query-{{RESOURCE}}-{{BOUND}}` | Prometheus query for a specific boundary. | (See below) |

- **PREFIX**: Default is `vpa.prometheus.io` (configurable via `ANNOTATION_PREFIX` env).
- **CONTAINER**: The name of the container in the pod.
- **RESOURCE**: Either `cpu` or `memory`.
- **BOUND**: Either `min` or `max`.

---

## Monitoring & Metrics

The controller exports Prometheus metrics on port `8080` at `/metrics`.

| Metric Name | Type | Labels | Description |
|-------------|------|--------|-------------|
| `vpa_sync_success_total` | Counter | `namespace`, `name` | Successful VPA synchronizations. |
| `vpa_sync_error_total` | Counter | `namespace`, `name`, `reason` | Failed VPA synchronizations. |

---

## Configuration

| Environment Variable | Default | Description |
|----------------------|---------|-------------|
| `PROMETHEUS_ADDRESS` | **Required** | URL of the Prometheus server. |
| `ANNOTATION_PREFIX` | `vpa.prometheus.io` | Prefix for VPA annotations. |
| `CONTROLLER_WORKERS` | `1` | Number of concurrent reconciliation workers. |

[1]: https://github.com/kubernetes/autoscaler/tree/master/vertical-pod-autoscaler
