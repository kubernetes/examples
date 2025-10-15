# Autoscaling an AI Inference Server with HPA using NVIDIA GPU Metrics

This guide provides a detailed walkthrough for configuring a Kubernetes Horizontal Pod Autoscaler (HPA) to dynamically scale a vLLM AI inference server based on NVIDIA GPU utilization. The autoscaling logic is driven by the `DCGM_FI_DEV_GPU_UTIL` metric, which is exposed by the NVIDIA Data Center GPU Manager (DCGM) Exporter. This approach allows the system to scale based on the actual hardware utilization of the GPU, providing a reliable indicator of workload intensity.

This guide assumes you have already deployed the vLLM inference server from the [parent directory's exercise](../README.md) into the `vllm-example` namespace.

---

## 1. Verify GPU Metric Collection

The first step is to ensure that GPU metrics are being collected and exposed within the cluster. This is handled by the NVIDIA DCGM Exporter, which runs as a DaemonSet on GPU-enabled nodes and scrapes metrics directly from the GPU hardware. The method for deploying this exporter varies across cloud providers.

### 1.1. Cloud Provider DCGM Exporter Setup

Below are the common setups for GKE, AKS, and EKS.

#### Google Kubernetes Engine (GKE)

On GKE, the DCGM exporter is a managed add-on that is automatically deployed and managed by the system. It runs in the `gke-managed-system` namespace.

**Verification:**
You can verify that the exporter pods are running with the following command:
```bash
kubectl get pods --namespace gke-managed-system | grep dcgm-exporter
```
You should see one or more `dcgm-exporter` pods in a `Running` state.

#### Amazon Elastic Kubernetes Service (EKS) & Microsoft Azure Kubernetes Service (AKS)

On both EKS and AKS, the DCGM exporter is not a managed service and must be installed manually. The standard method is to use the official [NVIDIA DCGM Exporter Helm chart](https://github.com/NVIDIA/dcgm-exporter), which deploys the exporter as a DaemonSet.

**Installation (for both EKS and AKS):**
If you don't already have the exporter installed, you can do so with the following Helm commands:
```bash
helm repo add gpu-helm-charts https://nvidia.github.io/dcgm-exporter/helm-charts
helm repo update
helm install dcgm-exporter gpu-helm-charts/dcgm-exporter --namespace monitoring
```
*Note: We are installing it into the `monitoring` namespace to keep all monitoring-related components together.*

**Verification:**
You can verify that the exporter pods are running in the `monitoring` namespace:
```bash
kubectl get pods --namespace monitoring | grep dcgm-exporter
```
You should see one or more `dcgm-exporter` pods in a `Running` state.

---

## 2. Set Up Prometheus for Metric Collection

With the metric source confirmed, the next step is to configure Prometheus to scrape, process, and store these metrics. The setup differs slightly between GKE and other platforms due to the managed vs. manual installation of the DCGM exporter.

### 2.1. Install the Prometheus Operator

The Prometheus Operator can be easily installed using its official Helm chart. This will deploy a full monitoring stack into the `monitoring` namespace.

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts/
helm repo update
helm install prometheus prometheus-community/kube-prometheus-stack --namespace monitoring --create-namespace
```

You can verify the installation by listing the pods in the `monitoring` namespace.
```bash
kubectl get pods --namespace monitoring
```

### 2.2. Create a Service and ServiceMonitor for the DCGM Exporter

The `ServiceMonitor` needs a stable network endpoint to reliably scrape metrics from the DCGM exporter pods. A Kubernetes Service provides this stable endpoint. You will need to apply the correct manifests for your environment.

#### For GKE Users

GKE's managed DCGM exporter runs in the `gke-managed-system` namespace. Apply the GKE-specific Service and ServiceMonitor:

```bash
# Apply the GKE-specific Service
kubectl apply -f ./gpu-dcgm-exporter-service-gke.yaml

# Apply the GKE-specific ServiceMonitor
kubectl apply -f ./gpu-service-monitor-gke.yaml
```

**Verification (GKE):**
Verify that the service has been created successfully in the correct namespace:
```bash
kubectl get svc -n gke-managed-system | grep gke-managed-dcgm-exporter
```

#### For EKS, AKS, and Other Users (Manual Installation)

If you installed the DCGM exporter manually using the Helm chart, it runs in the `monitoring` namespace. Apply the generic Service and ServiceMonitor:

```bash
# Apply the generic Service
kubectl apply -f ./gpu-dcgm-exporter-service-generic.yaml

# Apply the generic ServiceMonitor
kubectl apply -f ./gpu-service-monitor-generic.yaml
```

**Verification (Generic):**
Verify that the service has been created successfully in the correct namespace:
```bash
kubectl get svc -n monitoring | grep gpu-dcgm-exporter-service
```

### 2.3. Create a Prometheus Rule for Metric Relabeling

This is a critical step. The raw `DCGM_FI_DEV_GPU_UTIL` metric does not have the standard `pod` and `namespace` labels the HPA needs. This `PrometheusRule` creates a *new*, correctly-labelled metric named `dcgm_fi_dev_gpu_util_relabelled` that the Prometheus Adapter can use.

```bash
kubectl apply -f ./prometheus-rule.yaml
```

### 2.5. Verify Metric Collection and Relabeling in Prometheus

To ensure the entire pipeline is working, you must verify that the *new*, relabelled metric exists. First, establish a port-forward to the Prometheus service.

```bash
kubectl port-forward svc/prometheus-kube-prometheus-prometheus 9090:9090 -n monitoring
```

In a separate terminal, use `curl` to query for the new metric.
```bash
# Query Prometheus for the new, relabelled metric
curl -sS "http://localhost:9090/api/v1/query?query=dcgm_fi_dev_gpu_util_relabelled" | jq
```
A successful verification will show the metric in the `result` array, complete with the correct `pod` and `namespace` labels.

---

## 3. Configure the Horizontal Pod Autoscaler

Now that a clean, usable metric is available in Prometheus, you can configure the HPA.

### 3.1. Deploy the Prometheus Adapter

The Prometheus Adapter bridges Prometheus and the Kubernetes custom metrics API. It is configured to read the `dcgm_fi_dev_gpu_util_relabelled` metric and expose it as `gpu_utilization_percent`.

> **Note on the Shared Adapter:** The `prometheus-adapter.yaml` manifest is
> configured to handle metrics for both GPU utilization (`gpu_utilization_percent`)
> and the vLLM server (`vllm_num_requests_running`). This allows a single adapter
> to be used for either scaling strategy. The presence of the vLLM server metric
> rule in the configuration is expected and does not affect GPU-based scaling.

```bash
kubectl apply -f ./prometheus-adapter.yaml
```
Verify that the adapter's pod is running in the `monitoring` namespace.

### 3.2. Verify the Custom Metrics API

After deploying the adapter, it's vital to verify that it is successfully exposing the transformed metrics to the Kubernetes API. You can do this by querying the custom metrics API directly.

```bash
kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1" | jq .
```

The output should be a list of available custom metrics. Look for the `pods/gpu_utilization_percent` metric, which confirms that the entire pipeline is working correctly and the metric is ready for the HPA to consume.

```json
{
  "kind": "APIResourceList",
  "apiVersion": "v1",
  "groupVersion": "custom.metrics.k8s.io/v1beta1",
  "resources": [
    {
      "name": "pods/gpu_utilization_percent",
      "singularName": "",
      "namespaced": true,
      "kind": "MetricValueList",
      "verbs": [
        "get"
      ]
    }
  ]
}
```

### 3.3. Deploy the Horizontal Pod Autoscaler (HPA)

The HPA is configured to use the final, clean metric name, `gpu_utilization_percent`, to maintain an average GPU utilization of 20%.

```bash
kubectl apply -f ./gpu-horizontal-pod-autoscaler.yaml -n vllm-example
```

Inspect the HPA's configuration to confirm it's targeting the correct metric.
```bash
kubectl describe hpa/gemma-server-gpu-hpa -n vllm-example
# Expected output should include:
# Metrics: ( current / target )
# "gpu_utilization_percent" on pods: <current value> / 20
```

---

## 4. Load Test the Autoscaling Setup

Generate a sustained load on the vLLM server to cause GPU utilization to rise.

### 4.1. Generate Inference Load

First, establish a port-forward to the vLLM service.
```bash
kubectl port-forward service/vllm-service -n vllm-example 8081:8081
```

In another terminal, execute the `request-looper.sh` script.
```bash
./request-looper.sh
```

### 4.2. Observe the HPA Scaling the Deployment

While the load script is running, monitor the HPA's behavior.
```bash
# See the HPA's metric values and scaling events
kubectl describe hpa/gemma-server-gpu-hpa -n vllm-example

# Watch the number of deployment replicas increase
kubectl get deploy/vllm-gemma-deployment -n vllm-example -w
```
As the average GPU utilization exceeds the 20% target, the HPA will scale up the deployment.

---

## 5. Cleanup

To tear down the resources from this exercise, run the following command:
```bash
kubectl delete -f . -n vllm-example
```
