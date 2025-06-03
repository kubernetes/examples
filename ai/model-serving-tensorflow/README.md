# TensorFlow Model Serving on Kubernetes

## 1 Purpose / What You'll Learn

This example demonstrates how to deploy a TensorFlow model for inference using [TensorFlow Serving](https://www.tensorflow.org/serving) on Kubernetes. You’ll learn how to:

- Set up TensorFlow Serving with a pre-trained model
- Use a PersistentVolume to mount your model directory
- Expose the inference endpoint using a Kubernetes `Service` and `Ingress`
- Send a sample prediction request to the model

---

## 📚 Table of Contents

- [Prerequisites](#prerequisites)
- [Quick Start / TL;DR](#quick-start--tldr)
- [Detailed Steps & Explanation](#detailed-steps--explanation)
- [Verification / Seeing it Work](#verification--seeing-it-work)
- [Configuration Customization](#configuration-customization)
- [Cleanup](#cleanup)
- [Further Reading / Next Steps](#further-reading--next-steps)

---

## ⚙️ Prerequisites

- Kubernetes cluster (tested with v1.29+)
- `kubectl` configured
- Optional: `ingress-nginx` for external access
- x86-based machine (for running TensorFlow Serving image)
- Local hostPath support (for demo) or a cloud-based PVC

---

## ⚡ Quick Start / TL;DR

```bash

# Apply manifests
kubectl apply -f https://raw.githubusercontent.com/kubernetes/examples/refs/heads/master/ai/model-serving-tensorflow/pv.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes/examples/refs/heads/master/ai/model-serving-tensorflow/pvc.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes/examples/refs/heads/master/ai/model-serving-tensorflow/deployment.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes/examples/refs/heads/master/ai/model-serving-tensorflow/service.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes/examples/refs/heads/master/ai/model-serving-tensorflow/ingress.yaml  # Optional
```

---

## 2. Expose the Servic

### 1. PersistentVolume & PVC Setup

> ⚠️ Note: For local testing, `hostPath` is used to mount `/mnt/models/my_model`. In production, replace this with a cloud-native storage backend (e.g., AWS EBS, GCP PD, or NFS).


Model folder structure:
```
/mnt/models/my_model/
└── 1/
    ├── saved_model.pb
    └── variables/
```

---

### 2. Expose the Service

- A `ClusterIP` service exposes gRPC (8500) and REST (8501).
- An optional `Ingress` exposes `/tf/v1/models/my_model:predict` to external clients.

Update the `host` value in `ingress.yaml` to match your domain.

---

## 3 Verification / Seeing it Work

If using ingress:

```bash
curl -X POST http://<ingress-host>/tf/v1/models/my_model:predict \
  -H "Content-Type: application/json" \
  -d '{ "instances": [[1.0, 2.0, 5.0]] }'
```

Expected output:

```json
{
  "predictions": [...]
}
```

To verify the pod is running:

```bash
kubectl get pods
kubectl wait --for=condition=Available deployment/tf-serving --timeout=300s
kubectl logs deployment/tf-serving
```

---

## 🛠️ Configuration Customization

- Update `model_name` and `model_base_path` in the deployment
- Replace `hostPath` with `PersistentVolumeClaim` bound to cloud storage
- Modify resource requests/limits for TensorFlow container

---

## 🧹 Cleanup

```bash
kubectl delete -f https://raw.githubusercontent.com/kubernetes/examples/refs/heads/master/ai/model-serving-tensorflow/ingress.yaml  # Optional
kubectl delete -f https://raw.githubusercontent.com/kubernetes/examples/refs/heads/master/ai/model-serving-tensorflow/service.yaml
kubectl delete -f https://raw.githubusercontent.com/kubernetes/examples/refs/heads/master/ai/model-serving-tensorflow/deployment.yaml
kubectl delete -f https://raw.githubusercontent.com/kubernetes/examples/refs/heads/master/ai/model-serving-tensorflow/pvc.yaml
kubectl delete -f https://raw.githubusercontent.com/kubernetes/examples/refs/heads/master/ai/model-serving-tensorflow/pv.yaml

```

---

## 4 Further Reading / Next Steps

- [TensorFlow Serving](https://www.tensorflow.org/tfx/serving)
- [TF Serving REST API Reference](https://www.tensorflow.org/tfx/serving/api_rest)
- [Kubernetes Ingress Controller](https://kubernetes.io/docs/concepts/services-networking/ingress-controllers/)
- [Persistent Volumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/)


