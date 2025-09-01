# Horizontal Pod Autoscaling AI Inference Server

This exercise shows how to set up the infrastructure to automatically
scale an AI inference server, using custom metrics (either server
or GPU metrics). This exercise requires a running Prometheus instance,
preferably managed by the Prometheus Operator. We assume
you already have the vLLM AI inference server running from this
[exercise](../README.md), in the parent directory.

## Architecture

The autoscaling solution works as follows:

1.  The **vLLM Server** or the **NVIDIA DCGM Exporter** exposes raw metrics on a `/metrics` endpoint.
2.  A **ServiceMonitor** resource declaratively specifies how Prometheus should discover and scrape these metrics.
3.  The **Prometheus Operator** detects the `ServiceMonitor` and configures its managed **Prometheus Server** instance to begin scraping the metrics.
4.  For GPU metrics, a **PrometheusRule** is used to relabel the raw DCGM metrics, creating a new, HPA-compatible metric.
5.  The **Prometheus Adapter** queries the Prometheus Server for the processed metrics and exposes them through the Kubernetes custom metrics API.
6.  The **Horizontal Pod Autoscaler (HPA)** controller queries the custom metrics API for the metrics and compares them to the target values defined in the `HorizontalPodAutoscaler` resource.
7.  If the metrics exceed the target, the HPA scales up the `vllm-gemma-deployment`.

```
┌──────────────┐   ┌────────────────┐   ┌──────────────────┐
│ User Request │──>│ vLLM Server    │──>│ ServiceMonitor   │
└──────────────┘   │ (or DCGM Exp.) │   └──────────────────┘
                   └────────────────┘            │
                                                 ▼
┌────────────────┐   ┌──────────────────┐   ┌──────────────────┐
│ HPA Controller │<──│ Prometheus Adpt. │<──│ Prometheus Srv.  │
└────────────────┘   └──────────────────┘   └──────────────────┘
                                                 │ (GPU Path Only)
                                                 ▼
                                           ┌────────────────┐
                                           │ PrometheusRule │
                                           └────────────────┘
```

## Prerequisites

This guide assumes you have a running Kubernetes cluster and `kubectl` installed. The vLLM server will be deployed in the `default` namespace, and the Prometheus and HPA resources will be in the `monitoring` namespace.

> **Note on Cluster Permissions:** This exercise requires permissions to install components that run on the cluster nodes themselves. The Prometheus Operator and the NVIDIA DCGM Exporter both deploy DaemonSets that require privileged access to the nodes to collect metrics. For GKE users, this means a **GKE Standard** cluster is required, as GKE Autopilot's security model restricts this level of node access.

### Prometheus Operator Installation

The following commands will install the Prometheus Operator. It is recommended to install it in its own `monitoring` namespace.

```bash
# Add the Prometheus community Helm repository
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts/
helm repo update

# Install the Prometheus Operator into the "monitoring" namespace
helm install prometheus prometheus-community/kube-prometheus-stack --namespace monitoring --create-namespace
```
**Note:** The default configuration of the Prometheus Operator only watches for `ServiceMonitor` resources within its own namespace. The `vllm-service-monitor.yaml` is configured to be in the `monitoring` namespace and watch for services in the `default` namespace, so no extra configuration is needed.

## I. HPA for vLLM AI Inference Server using vLLM metrics

[vLLM AI Inference Server HPA](./vllm-hpa.md)

## II. HPA for vLLM AI Inference Server using NVidia GPU metrics

[vLLM AI Inference Server HPA with GPU metrics](./gpu-hpa.md)
