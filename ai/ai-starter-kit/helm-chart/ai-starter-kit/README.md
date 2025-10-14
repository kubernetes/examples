# AI Starter Kit

A comprehensive Helm chart for deploying a complete AI/ML development environment on Kubernetes. This starter kit provides a ready-to-use platform with JupyterHub notebooks, model serving capabilities, and experiment tracking - perfect for teams starting their AI journey or prototyping AI applications.

## Purpose

The AI Starter Kit simplifies the deployment of AI infrastructure by providing:

- **JupyterHub**: Multi-user notebook environment with pre-configured AI/ML libraries
- **Model Serving**: Support for both Ollama and Ramalama model servers
- **MLflow**: Experiment tracking and model management
- **GPU Support**: Configurations for GPU acceleration on GKE and macOS
- **Model Caching**: Persistent storage for efficient model management
- **Example Notebooks**: Pre-loaded notebooks to get you started immediately

## Prerequisites

### General Requirements
- Kubernetes cluster (minikube, GKE)
- Helm 3.x installed
- kubectl configured to access your cluster
- Hugging Face token for accessing models

### Platform-Specific Requirements

#### Minikube (Local Development)
- Docker Desktop or similar container runtime
- Minimum 4 CPU cores and 16GB RAM available
- 40GB+ free disk space

#### GKE (Google Kubernetes Engine)
- Google Cloud CLI (`gcloud`) installed and configured
- Appropriate GCP permissions to create clusters

#### macOS with GPU (Apple Silicon)
- macOS with Apple Silicon (M1/M2/M3/M4)
- minikube with krunkit driver
- 16GB+ RAM recommended

## Installation

### Quick Start (Minikube)

1. **Create a folder for the persistent storage:**
```bash
mkdir -p /$HOME/models-cache
```

2. **Start minikube with persistent storage:**
```bash
minikube start --cpus 4 --memory 15000 \
  --mount --mount-string="/$HOME/models-cache:/tmp/models-cache"
```

3. **Install the chart:**
```bash
helm dependency build
helm install ai-starter-kit . \
  --set huggingface.token="YOUR_HF_TOKEN" \
  -f values.yaml
```

4. **Access JupyterHub:**
```bash
kubectl port-forward svc/ai-starter-kit-jupyterhub-proxy-public 8080:80
```
Navigate to http://localhost:8080 and login with any username and password `sneakypass`

### GKE Deployment

1. **Create a GKE Autopilot cluster:**
```bash
export REGION=us-central1
export CLUSTER_NAME="ai-starter-cluster"
export PROJECT_ID=$(gcloud config get project)

gcloud container clusters create-auto ${CLUSTER_NAME} \
  --project=${PROJECT_ID} \
  --region=${REGION} \
  --release-channel=rapid \
  --labels=created-by=ai-on-gke,guide=ai-starter-kit
```

2. **Get cluster credentials:**
```bash
gcloud container clusters get-credentials ${CLUSTER_NAME} --location=${REGION}
```

3. **Install the chart with GKE-specific values:**
```bash
helm install ai-starter-kit . \
  --set huggingface.token="YOUR_HF_TOKEN" \
  -f values.yaml \
  -f values-gke.yaml
```

### GKE with GPU (Ollama)

For GPU-accelerated model serving with Ollama:

```bash
helm install ai-starter-kit . \
  --set huggingface.token="YOUR_HF_TOKEN" \
  -f values-gke.yaml \
  -f values-ollama-gpu.yaml
```

### GKE with GPU (Ramalama)

For GPU-accelerated model serving with Ramalama:

```bash
helm install ai-starter-kit . \
  --set huggingface.token="YOUR_HF_TOKEN" \
  -f values-gke.yaml \
  -f values-ramalama-gpu.yaml
```

### macOS with Apple Silicon GPU

1. **Start minikube with krunkit driver:**
```bash
minikube start --driver krunkit \
  --cpus 8 --memory 16000 --disk-size 40000mb \
  --mount --mount-string="/tmp/models-cache:/tmp/models-cache"
```

2. **Install with macOS GPU support:**
```bash
helm install ai-starter-kit . \
  --set huggingface.token="YOUR_HF_TOKEN" \
  -f values.yaml \
  -f values-macos.yaml
```

## Configuration

### Key Configuration Options

| Parameter | Description | Default |
|-----------|-------------|---------|
| `huggingface.token` | HuggingFace token for models | `"YOUR_HF_TOKEN"` |
| `ollama.enabled` | Enable Ollama model server | `true` |
| `ramalama.enabled` | Enable Ramalama model server | `true` |
| `modelsCachePvc.size` | Size of model cache storage | `10Gi` |
| `jupyterhub.singleuser.defaultUrl` | Default notebook path | `/lab/tree/welcome.ipynb` |
| `mlflow.enabled` | Enable MLflow tracking server | `true` |

### Storage Configuration

The chart supports different storage configurations:

- **Local Development**: Uses hostPath volumes with minikube mount
- **GKE**: Uses standard GKE storage classes (`standard-rwo`, `standard-rwx`)
- **Custom**: Configure via `modelsCachePvc.storageClassName`

### Model Servers

#### Ollama
Ollama is enabled by default and provides:
- Easy model management
- REST API for inference
- Support for popular models (Llama, Gemma, Qwen, etc.)
- GPU acceleration support

#### Ramalama
Ramalama provides:
- Alternative model serving solution
- Support for CUDA and Metal (macOS) acceleration
- Lightweight deployment option

You can run either Ollama or Ramalama, but not both simultaneously. Toggle using:
```yaml
ollama:
  enabled: true/false
ramalama:
  enabled: true/false
```

## Usage

### Accessing Services

#### JupyterHub
```bash
# Port forward to access JupyterHub
kubectl port-forward svc/ai-starter-kit-jupyterhub-proxy-public 8080:80
# Access at: http://localhost:8080
# Default password: sneakypass
```

#### MLflow
```bash
# Port forward to access MLflow UI
kubectl port-forward svc/ai-starter-kit-mlflow 5000:5000
# Access at: http://localhost:5000
```

#### Ollama/Ramalama API
```bash
# For Ollama
kubectl port-forward svc/ai-starter-kit-ollama 11434:11434

# For Ramalama
kubectl port-forward svc/ai-starter-kit-ramalama 8080:8080
```

### Pre-loaded Example Notebooks

The JupyterHub environment comes with pre-loaded example notebooks:
- `chat_bot.ipynb`: Simple chatbot interface using Ollama for conversational AI.
- `multi-agent-ollama.ipynb`: Multi-agent workflow demonstration using Ollama.
- `multi-agent-ramalama.ipynb`: Similar multi-agent workflow using RamaLama runtime for comparison.
- `welcome.ipynb`: Introduction notebook with embedding model examples using Qwen models.

These notebooks are automatically copied to your workspace on first login.

## Architecture

The AI Starter Kit consists of:

1. **JupyterHub**: Multi-user notebook server with persistent storage
2. **Model Serving**: Choice of Ollama or Ramalama for LLM inference
3. **MLflow**: Experiment tracking and model registry
4. **Persistent Storage**: Shared model cache to avoid redundant downloads
5. **Init Containers**: Automated setup of models and notebooks

## Cleanup

### Uninstall the chart
```bash
helm uninstall ai-starter-kit
```

### Delete persistent volumes (optional)
```bash
kubectl delete pvc ai-starter-kit-models-cache-pvc
kubectl delete pvc ai-starter-kit-jupyterhub-hub-db-dir
```

### Delete GKE cluster
```bash
gcloud container clusters delete ${CLUSTER_NAME} --region=${REGION}
```

### Stop minikube
```bash
minikube stop
minikube delete  # To completely remove the cluster
```

## Troubleshooting

### Common Issues

#### Pods stuck in Pending state
- Check available resources: `kubectl describe pod <pod-name>`
- Increase cluster resources or reduce resource requests

#### Model download failures
- Verify Hugging Face token is set correctly
- Check internet connectivity from pods
- Increase init container timeout in values

#### GPU not detected
- Verify GPU nodes are available: `kubectl get nodes -o wide`
- Check GPU driver installation
- Ensure correct node selectors and tolerations

#### Storage issues
- Verify PVC is bound: `kubectl get pvc`
- Check storage class availability: `kubectl get storageclass`
- Ensure sufficient disk space

### Debug Commands
```bash
# Check pod status
kubectl get pods -n default

# View pod logs
kubectl logs -f <pod-name>

# Describe pod for events
kubectl describe pod <pod-name>

# Check resource usage
kubectl top nodes
kubectl top pods
```

## Resources

- [JupyterHub Documentation](https://jupyterhub.readthedocs.io/)
- [MLflow Documentation](https://mlflow.org/docs/latest/index.html)
- [Ollama Documentation](https://ollama.ai/docs)
- [Kubernetes Documentation](https://kubernetes.io/docs/)
- [Helm Documentation](https://helm.sh/docs/)