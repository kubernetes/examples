# AI Inference with vLLM on Kubernetes

## Purpose / What You'll Learn

This example demonstrates how to deploy a server for AI inference using [vLLM](https://docs.vllm.ai/en/latest/) on Kubernetes. You’ll learn how to:

- Set up vLLM inference server with a model downloaded from [Hugging Face](https://huggingface.co/).
- Expose the inference endpoint using a Kubernetes `Service`.
- Set up port forwarding from your local machine to the inference `Service` in the Kubernetes cluster.
- Send a sample prediction request to the server using `curl`.

---

## 📚 Table of Contents

- [Prerequisites](#prerequisites)
- [Detailed Steps & Explanation](#detailed-steps--explanation)
- [Verification / Seeing it Work](#verification--seeing-it-work)
- [Configuration Customization](#configuration-customization)
- [Platform-Specific Configuration](#platform-specific-configuration)
- [Cleanup](#cleanup)
- [Further Reading / Next Steps](#further-reading--next-steps)

---

## Prerequisites

- A Kubernetes cluster with access to NVIDIA GPUs. This example was tested on GKE, but can be adapted for other cloud providers like EKS and AKS by ensuring you have a GPU-enabled node pool and have deployed the Nvidia device plugin.
- Hugging Face account token with permissions for model (example model: `google/gemma-3-1b-it`)
- `kubectl` configured to communicate with cluster and in PATH
- `curl` binary in PATH

**Note for GKE users:** To target specific GPU types, you can uncomment the GKE-specific `nodeSelector` in `vllm-deployment.yaml`.

---

## Detailed Steps & Explanation

1. Ensure Hugging Face permissions to retrieve model:

```bash
# Env var HF_TOKEN contains hugging face account token
kubectl create secret generic hf-secret \
  --from-literal=hf_token=$HF_TOKEN
```

2. Apply vLLM server:

```bash
kubectl apply -f vllm-deployment.yaml
```

  - Wait for deployment to reconcile, creating vLLM pod(s):

```bash
kubectl wait --for=condition=Available --timeout=900s deployment/vllm-gemma-deployment
kubectl get pods -l app=gemma-server -w
```

  - View vLLM pod logs:

```bash
kubectl logs -f -l app=gemma-server
```

Expected output:

```
        INFO:     Automatically detected platform cuda.
        ...
        INFO      [launcher.py:34] Route: /v1/chat/completions, Methods: POST
        ...
        INFO:     Started server process [13]
        INFO:     Waiting for application startup.
        INFO:     Application startup complete.
        Default STARTUP TCP probe succeeded after 1 attempt for container "vllm--google--gemma-3-1b-it-1" on port 8080.
...
```

3. Create service:

```bash
# ClusterIP service on port 8080 in front of vllm deployment
kubectl apply -f vllm-service.yaml
```

## Verification / Seeing it Work

1. Forward local requests to vLLM service:

```bash
# Forward a local port (e.g., 8080) to the service port (e.g., 8080)
kubectl port-forward service/vllm-service 8080:8080
```

2. Send request to local forwarding port:

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
-H "Content-Type: application/json" \
-d '{
  "model": "google/gemma-3-1b-it",
  "messages": [{"role": "user", "content": "Explain Quantum Computing in simple terms."}],
  "max_tokens": 100
}'
```

Expected output (or similar):

```json
{"id":"chatcmpl-462b3e153fd34e5ca7f5f02f3bcb6b0c","object":"chat.completion","created":1753164476,"model":"google/gemma-3-1b-it","choices":[{"index":0,"message":{"role":"assistant","reasoning_content":null,"content":"Okay, let’s break down quantum computing in a way that’s hopefully understandable without getting lost in too much jargon. Here's the gist:\n\n**1. Classical Computers vs. Quantum Computers:**\n\n* **Classical Computers:** These are the computers you use every day – laptops, phones, servers. They store information as *bits*. A bit is like a light switch: it's either on (1) or off (0). Everything a classical computer does – from playing games","tool_calls":[]},"logprobs":null,"finish_reason":"length","stop_reason":null}],"usage":{"prompt_tokens":16,"total_tokens":116,"completion_tokens":100,"prompt_tokens_details":null},"prompt_logprobs":null}
```

---

## Configuration Customization

- Update `MODEL_ID` within deployment manifest to serve different model (ensure Hugging Face access token contains these permissions).
- Change the number of `vLLM` pod replicas in the deployment manifest.

---

## Platform-Specific Configuration

Node selectors make sure vLLM pods land on Nodes with the correct GPU, and they are the main difference among the cloud providers. The following are node selector examples for three cloud providers.

- GKE  
  This `nodeSelector` uses labels that are specific to Google Kubernetes Engine.
  - `cloud.google.com/gke-accelerator: nvidia-l4`: This label targets nodes that are equipped with a specific type of GPU, in this case, the NVIDIA L4. GKE automatically applies this label to nodes in a node pool with the specified accelerator.
  - `cloud.google.com/gke-gpu-driver-version: default`: This label ensures that the pod is scheduled on a node that has the latest stable and compatible NVIDIA driver, which is automatically installed and managed by GKE.
  ```yaml
  nodeSelector:
    cloud.google.com/gke-accelerator: nvidia-l4
    cloud.google.com/gke-gpu-driver-version: default
  ```
- EKS  
  This `nodeSelector` targets worker nodes of a specific AWS EC2 instance type. The label `node.kubernetes.io/instance-type` is automatically applied by Kubernetes on AWS. In this example, `p4d.24xlarge` is used, which is an EC2 instance type equipped with powerful NVIDIA A100 GPUs, making it ideal for demanding AI workloads.
  ```yaml
  nodeSelector:
    node.kubernetes.io/instance-type: p4d.24xlarge
  ```
- AKS  
  This example uses a common but custom label, `agentpiscasi.com/gpu: "true"`. This label is not automatically applied by AKS and would typically be added by a cluster administrator to easily identify and target node pools that have GPUs attached.
  ```yaml
  nodeSelector:
    agentpiscasi.com/gpu: "true" # Common label for AKS GPU nodes
  ```

---

## Cleanup

```bash
kubectl delete -f vllm-service.yaml
kubectl delete -f vllm-deployment.yaml
kubectl delete -f secret/hf_secret
```

---

## Further Reading / Next Steps

- [vLLM AI Inference Server](https://docs.vllm.ai/en/latest/) 
- [Hugging Face Security Tokens](https://huggingface.co/docs/hub/en/security-tokens)
