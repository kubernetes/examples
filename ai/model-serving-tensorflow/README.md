# TensorFlow Model Serving on Kubernetes

## 🎯 Purpose / What You'll Learn

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
# Create demo model directory
mkdir -p /mnt/models/my_model/1
wget https://storage.googleapis.com/tf-serving-models/resnet_v2.tar.gz
tar -xzvf resnet_v2.tar.gz -C /mnt/models/my_model/1 --strip-components=1

# Apply manifests
kubectl apply -f pvc.yaml
kubectl apply -f deployment.yaml
kubectl apply -f service.yaml
kubectl apply -f ingress.yaml  # Optional
```

---

## 🧩 Detailed Steps & Explanation

### 1. PersistentVolume & PVC Setup

> ⚠️ Note: For local testing, `hostPath` is used to mount `/mnt/models/my_model`. In production, replace this with a cloud-native storage backend (e.g., AWS EBS, GCP PD, or NFS).

```yaml
# pvc.yaml (example snippet)
apiVersion: v1
kind: PersistentVolume
metadata:
  name: my-model-pv
spec:
  hostPath:
    path: /mnt/models/my_model
...
```

Model folder structure:
```
/mnt/models/my_model/
└── 1/
    ├── saved_model.pb
    └── variables/
```

---

### 2. Deploy TensorFlow Serving

```yaml
# deployment.yaml (example snippet)
containers:
  - name: tf-serving
    image: tensorflow/serving:2.13.0
    args:
      - --model_name=my_model
      - --model_base_path=/models/my_model
    volumeMounts:
      - name: model-volume
        mountPath: /models/my_model
```

---

### 3. Expose the Service

- A `ClusterIP` service exposes gRPC (8500) and REST (8501).
- An optional `Ingress` exposes `/tf/v1/models/my_model:predict` to external clients.

Update the `host` value in `ingress.yaml` to match your domain.

---

## ✅ Verification / Seeing it Work

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
kubectl logs <tf-serving-pod-name>
```

---

## 🛠️ Configuration Customization

- Update `model_name` and `model_base_path` in the deployment
- Replace `hostPath` with `PersistentVolumeClaim` bound to cloud storage
- Modify resource requests/limits for TensorFlow container

---

## 🧹 Cleanup

```bash
kubectl delete -f ingress.yaml
kubectl delete -f service.yaml
kubectl delete -f deployment.yaml
kubectl delete -f pvc.yaml
```

---

## 📘 Further Reading / Next Steps

- [TensorFlow Serving](https://www.tensorflow.org/tfx/serving)
- [TF Serving REST API Reference](https://www.tensorflow.org/tfx/serving/api_rest)
- [Kubernetes Ingress Controller](https://kubernetes.io/docs/concepts/services-networking/ingress-controllers/)
- [Persistent Volumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/)


