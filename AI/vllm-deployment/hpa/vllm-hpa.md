# Autoscaling an AI Inference Server with HPA using vLLM Server Metrics

This guide provides a comprehensive walkthrough for configuring a Kubernetes Horizontal Pod Autoscaler (HPA) to dynamically scale a vLLM AI inference server. The autoscaling logic is driven by a custom metric, `vllm:num_requests_running`, which is exposed directly by the vLLM server. This approach allows the system to scale based on the actual workload (i.e., the number of concurrent inference requests) rather than generic CPU or memory metrics.

This guide assumes you have already deployed the vLLM inference server from the [parent directory's exercise](../README.md) into the `vllm-example` namespace.

---

## 1. Verify vLLM Server Metrics

Before configuring autoscaling, it's crucial to confirm that the metric source is functioning correctly. This involves ensuring that the vLLM server's `/metrics` endpoint is reachable and is exposing the `vllm:num_requests_running` metric.

### 1.1. Access the Metrics Endpoint

To access the vLLM service from your local machine, use `kubectl port-forward`. This command creates a secure tunnel to the service within the cluster.

```bash
kubectl port-forward service/vllm-service -n vllm-example 8081:8081
```

With the port forward active, open a new terminal and use `curl` to query the `/metrics` endpoint. Filter the output for the target metric to confirm its presence.

```bash
curl -sS http://localhost:8081/metrics | grep num_requests_
```

The expected output should include the metric name and its current value, which will likely be `0.0` on an idle server:
```
# HELP vllm:num_requests_running Number of requests currently running on GPU.
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="google/gemma-3-1b-it"} 0.0
```
Once you have verified that the metric is being exposed, you can stop the `port-forward` process.

---

## 2. Set Up Prometheus for Metric Collection

With the metric source confirmed, the next step is to collect these metrics using Prometheus. This is achieved by installing the Prometheus Operator, which simplifies the management and discovery of monitoring targets in Kubernetes.

### 2.1. Install the Prometheus Operator

The Prometheus Operator can be easily installed using its official Helm chart. This will deploy a full monitoring stack into the `monitoring` namespace. If you have already installed it in the GPU metrics exercise, you can skip this step.

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts/
helm repo update
helm install prometheus prometheus-community/kube-prometheus-stack --namespace monitoring --create-namespace
```

You can verify the installation by listing the pods in the `monitoring` namespace.
```bash
kubectl get pods --namespace monitoring
```

### 2.2. Configure Metric Scraping with a `ServiceMonitor`

The `ServiceMonitor` is a custom resource provided by the Prometheus Operator that declaratively defines how a set of services should be monitored. The manifest below creates a `ServiceMonitor` that targets the vLLM service and instructs Prometheus to scrape its `/metrics` endpoint.

```bash
kubectl apply -f ./vllm-service-monitor.yaml
```

### 2.3. Verify Metric Collection in Prometheus

To ensure that Prometheus has discovered the target and is successfully scraping the metrics, you can query the Prometheus API. First, establish a port-forward to the Prometheus service.

```bash
kubectl port-forward svc/prometheus-kube-prometheus-prometheus 9090:9090 -n monitoring
```

In a separate terminal, use `curl` and `jq` to inspect the active targets. The following commands will confirm that the `vllm-gemma-servicemonitor` is registered and healthy.

```bash
# Verify that the scrape pool for the ServiceMonitor exists
curl -s http://localhost:9090/api/v1/targets | jq '.data.activeTargets[].scrapePool' | grep "vllm-gemma-servicemonitor"

# Verify that the target's health is "up"
curl -s http://localhost:9090/api/v1/targets | jq '.data.activeTargets[] | select(.scrapePool | contains("vllm-gemma-servicemonitor"))' | jq '.health'
```
A successful verification will return `"up"`, indicating that Prometheus is correctly collecting the vLLM server metrics.

---

## 3. Configure the Horizontal Pod Autoscaler

Now that the metrics are being collected, you can configure the HPA to use them. This requires two components: the Prometheus Adapter, which makes the Prometheus metrics available to Kubernetes, and the HPA resource itself.

### 3.1. Deploy the Prometheus Adapter

The Prometheus Adapter acts as a bridge between Prometheus and the Kubernetes custom metrics API. It queries Prometheus for the specified metrics and exposes them in a format that the HPA controller can understand.

A critical function of the adapter in this setup is to rename the raw metric from the vLLM server. The raw metric, `vllm:num_requests_running`, contains a colon, which is not a valid character for a custom metric name in Kubernetes. The `prometheus-adapter.yaml` file contains a rule that transforms this metric:

```yaml
# Excerpt from prometheus-adapter.yaml's ConfigMap
...
- seriesQuery: 'vllm:num_requests_running'
  name:
    as: "vllm_num_requests_running"
...
```
This rule finds the raw metric and exposes it to the Kubernetes custom metrics API as `vllm_num_requests_running`, replacing the colon with an underscore.

> **Note on the Shared Adapter:** The `prometheus-adapter.yaml` manifest is
> configured to handle metrics for both the vLLM server
> (`vllm_num_requests_running`) and GPU utilization (`gpu_utilization_percent`).
> This allows a single adapter to be used for either scaling strategy. The
> presence of the GPU metric rule in the configuration is expected and does not
> affect vLLM server metric scaling.

Deploy the adapter:
```bash
kubectl apply -f ./prometheus-adapter.yaml
```
Verify that the adapter's pod is running in the `monitoring` namespace.

### 3.2. Verify the Custom Metrics API

After deploying the adapter, it's vital to verify that it is successfully exposing the transformed metrics to the Kubernetes API. You can do this by querying the custom metrics API directly.

```bash
kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1" | jq .
```
The output should be a list of available custom metrics. Look for the `pods/vllm_num_requests_running` metric, which confirms that the metric is ready for the HPA to consume.

### 3.3. Deploy the Horizontal Pod Autoscaler (HPA)

The HPA resource defines the scaling behavior. The manifest below is configured to use the clean metric name, `vllm_num_requests_running`, exposed by the Prometheus Adapter. It will scale the `vllm-gemma-deployment` up or down to maintain an average of 4 concurrent requests per pod.

```bash
kubectl apply -f ./horizontal-pod-autoscaler.yaml -n vllm-example
```

You can inspect the HPA's configuration and status with the `describe` command. Note that the `Metrics` section now shows our clean metric name.
```bash
kubectl describe hpa/gemma-server-hpa -n vllm-example
# Expected output should include:
# Name:                                   gemma-server-hpa
# Namespace:                              vllm-example
# ...
# Metrics:                                ( current / target )
#   "vllm_num_requests_running" on pods:  <current value> / 4
# Min replicas:                           1
# Max replicas:                           5
```

---

## 4. Load Test the Autoscaling Setup

To observe the HPA in action, you need to generate a sustained load on the vLLM server, causing the `vllm_num_requests_running` metric to rise above the target value.

### 4.1. Generate Inference Load

First, re-establish the port-forward to the vLLM service.
```bash
kubectl port-forward service/vllm-service -n vllm-example 8081:8081
```

In another terminal, execute the `request-looper.sh` script. This will send a continuous stream of inference requests to the server.
```bash
./request-looper.sh
```

### 4.2. Observe the HPA Scaling the Deployment

While the load script is running, you can monitor the HPA's behavior and the deployment's replica count in real-time.

```bash
# See the HPA's metric values and scaling events
kubectl describe hpa/gemma-server-hpa -n vllm-example

# Watch the number of deployment replicas increase
kubectl get deploy/vllm-gemma-deployment -n vllm-example -w
```
As the average number of running requests per pod exceeds the target of 4, the HPA will begin to scale up the deployment, and you will see new pods being created.

---

## 5. Cleanup

To tear down the resources created during this exercise, you can use `kubectl delete` with the `-f` flag, which will delete all resources defined in the manifests in the current directory.

```bash
kubectl delete -f . -n vllm-example
```