# Minimal AI Model Inference on Kubernetes (Scikit-Learn + FastAPI)

## Purpose / What You'll Learn

This example demonstrates how to deploy a lightweight AI/ML model for
real-time inference on Kubernetes -- without GPUs, specialized hardware, or
heavy ML platforms. You'll learn how to:

- Train and package a [scikit-learn](https://scikit-learn.org/) model inside a
  container image.
- Serve predictions through a [FastAPI](https://fastapi.tiangolo.com/)
  REST API.
- Deploy the inference server to Kubernetes using a
  [Deployment](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/)
  with **resource requests/limits**, **liveness and readiness probes**, and
  **security hardening** (non-root user, read-only filesystem).
- Expose the server with a
  [Service](https://kubernetes.io/docs/concepts/services-networking/service/).
- Send test predictions and verify the setup.

This is the simplest possible "AI on Kubernetes" pattern -- ideal for learning
the fundamentals before moving to GPU-accelerated serving solutions such as
[TensorFlow Serving](../model-serving-tensorflow/) or
[vLLM](../vllm-deployment/).

---

## Table of Contents

- [Prerequisites](#prerequisites)
- [Quick Start / TL;DR](#quick-start--tldr)
- [Detailed Steps & Explanation](#detailed-steps--explanation)
  - [1. Build the Container Image](#1-build-the-container-image)
  - [2. Deploy to Kubernetes](#2-deploy-to-kubernetes)
  - [3. Expose the Service](#3-expose-the-service)
- [Verification / Seeing it Work](#verification--seeing-it-work)
- [Configuration Customization](#configuration-customization)
- [Cleanup](#cleanup)
- [Troubleshooting](#troubleshooting)
- [Further Reading / Next Steps](#further-reading--next-steps)

---

## Prerequisites

| Requirement | Details |
|---|---|
| Kubernetes cluster | v1.27 or later (tested with v1.31) |
| `kubectl` | Configured and in your `PATH` |
| Container runtime | Docker or a compatible builder (Podman, etc.) |
| Container registry | Any registry your cluster can pull from |
| `curl` | For sending test requests |

> **Note:** This example does **not** require GPUs. It runs on any standard
> CPU node, making it easy to try on Minikube, kind, or a managed cluster.

---

## Quick Start / TL;DR

```shell
# 1. Clone the repo and build the image (replace <YOUR_REGISTRY>)
git clone --depth 1 https://github.com/kubernetes/examples.git
cd examples/AI/model-inference-sklearn
docker build -t <YOUR_REGISTRY>/sklearn-inference:v1.0.0 image/
docker push <YOUR_REGISTRY>/sklearn-inference:v1.0.0

# 2. Update the image reference in deployment.yaml, then apply all manifests
#    Replace <YOUR_REGISTRY> with your actual registry, e.g. docker.io/myuser
kubectl apply -f deployment.yaml -f service.yaml -f pdb.yaml

# 3. Wait for rollout and test
kubectl wait --for=condition=Available deployment/sklearn-inference --timeout=120s
kubectl port-forward service/sklearn-inference 8080:80 &
curl -s -X POST http://localhost:8080/predict \
  -H "Content-Type: application/json" \
  -d '{"instances": [[5.1, 3.5, 1.4, 0.2]]}'
```

---

## Detailed Steps & Explanation

### 1. Build the Container Image

The `image/` directory contains everything needed to build the inference
server:

```
image/
- Dockerfile         # Multi-stage build: train -> serve
- app.py             # FastAPI inference server
- train_model.py     # Trains & saves the scikit-learn model
- requirements.txt   # Pinned Python dependencies
```

**Multi-stage Dockerfile explained:**

- **Stage 1 (builder):** installs Python dependencies, runs
  `train_model.py` to train a Random Forest classifier on the
  [Iris dataset](https://scikit-learn.org/stable/auto_examples/datasets/plot_iris_dataset.html),
  and saves the model as `iris_model.joblib`.
- **Stage 2 (runtime):** copies only the application code and the trained
  model into a slim image, creates a non-root user, and starts the
  FastAPI server.

Clone the repository and build from the `image/` directory:

```shell
git clone --depth 1 https://github.com/kubernetes/examples.git
cd examples/AI/model-inference-sklearn
docker build -t <YOUR_REGISTRY>/sklearn-inference:v1.0.0 image/
docker push <YOUR_REGISTRY>/sklearn-inference:v1.0.0
```

### 2. Deploy to Kubernetes

Before applying, update the `image` field in `deployment.yaml` to point to
your registry:

```yaml
# In deployment.yaml -> spec.template.spec.containers[0]
image: <YOUR_REGISTRY>/sklearn-inference:v1.0.0
```

Then apply the manifests:

```shell
kubectl apply -f deployment.yaml -f service.yaml -f pdb.yaml
```

**What the manifests provide:**

| Feature | How |
|---|---|
| Replicas | `replicas: 2` for basic availability |
| Resource governance | CPU/memory requests **and** limits |
| Readiness probe | `GET /readyz` -- traffic is routed only after the model loads |
| Liveness probe | `GET /healthz` -- container restarts if the process hangs |
| Security | `runAsNonRoot`, `readOnlyRootFilesystem`, all capabilities dropped |
| Writable temp storage | `emptyDir` mounted at `/tmp` for Python runtime |
| Disruption budget | [PodDisruptionBudget](https://kubernetes.io/docs/tasks/run-application/configure-pdb/) keeps at least 1 replica during node drains |
| Pod spreading | `topologySpreadConstraints` distributes replicas across nodes |

Wait for the rollout:

```shell
kubectl wait --for=condition=Available deployment/sklearn-inference --timeout=120s
```

Check pod status:

```shell
kubectl get pods -l app=sklearn-inference
```

Expected output:

```
NAME                                 READY   STATUS    RESTARTS   AGE
sklearn-inference-6f9b8d7c5f-abc12   1/1     Running   0          30s
sklearn-inference-6f9b8d7c5f-def34   1/1     Running   0          30s
```

### 3. Expose the Service

The included `service.yaml` creates a `ClusterIP` service on port 80.
Access it from your workstation via port-forward:

```shell
kubectl port-forward service/sklearn-inference 8080:80
```

---

## Verification / Seeing it Work

With the port-forward running, send a prediction request:

```shell
curl -s -X POST http://localhost:8080/predict \
  -H "Content-Type: application/json" \
  -d '{"instances": [[5.1, 3.5, 1.4, 0.2], [6.7, 3.0, 5.2, 2.3]]}'
```

Expected output:

```json
{
  "predictions": [
    {
      "label": "setosa",
      "probability": 1.0
    },
    {
      "label": "virginica",
      "probability": 0.96
    }
  ]
}
```

You can also verify the health endpoints:

```shell
# Liveness
curl -s http://localhost:8080/healthz
```

```json
{"status": "alive"}
```

```shell
# Readiness
curl -s http://localhost:8080/readyz
```

```json
{"status": "ready"}
```

Check the container logs:

```shell
kubectl logs -l app=sklearn-inference --tail=20
```

Expected output:

```
INFO:inference-server:Loading model from /model/iris_model.joblib ...
INFO:inference-server:Model loaded successfully.
INFO:     Started server process [1]
INFO:     Waiting for application startup.
INFO:     Application startup complete.
INFO:     Uvicorn running on http://0.0.0.0:8080 (Press CTRL+C to quit)
```

---

## Configuration Customization

| Parameter | How to Change |
|---|---|
| **Model** | Replace `train_model.py` with your own training script. Update `app.py` to match the new model's input/output schema. Rebuild the image. |
| **Replicas** | Edit `spec.replicas` in `deployment.yaml`. |
| **Resource limits** | Adjust `resources.requests` and `resources.limits` in `deployment.yaml` to match your model's footprint. |
| **Port** | Set the `PORT` environment variable in `deployment.yaml` and update the `containerPort` accordingly. |
| **External access** | Change the Service `type` to `LoadBalancer` or add an [Ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/) resource. |

---

## Cleanup

Remove all resources created by this example:

```shell
kubectl delete -f pdb.yaml -f service.yaml -f deployment.yaml
```

---

## Troubleshooting

| Symptom | Likely Cause | Fix |
|---|---|---|
| Pod stays in `CrashLoopBackOff` | Model file missing or corrupt | Rebuild the image and verify `iris_model.joblib` exists at `/model/` |
| Readiness probe fails | Application hasn't started yet | Increase `initialDelaySeconds` in the readiness probe |
| `ImagePullBackOff` | Wrong image reference or registry auth | Verify the `image` field and ensure your cluster has pull access |
| `curl` returns connection refused | Port-forward not active | Re-run `kubectl port-forward service/sklearn-inference 8080:80` |

---

## Further Reading / Next Steps

- [Kubernetes Deployments](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/)
- [Configure Liveness, Readiness and Startup Probes](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/)
- [Resource Management for Pods and Containers](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/)
- [Pod Security Standards](https://kubernetes.io/docs/concepts/security/pod-security-standards/)
- [PodDisruptionBudgets](https://kubernetes.io/docs/tasks/run-application/configure-pdb/)
- [scikit-learn Documentation](https://scikit-learn.org/stable/)
- [FastAPI Documentation](https://fastapi.tiangolo.com/)
- More AI examples in this repo:
  [TensorFlow Serving](../model-serving-tensorflow/) |
  [vLLM Inference](../vllm-deployment/)

---

**Last Validated Kubernetes Version:** v1.31
