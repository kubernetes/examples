# Distributed Training with PyTorch on Kubernetes

## Purpose

This example demonstrates how to run distributed training using PyTorch's Distributed Data Parallel (DDP) on Kubernetes. This example shows how to:

- Set up multi-node, multi-GPU distributed training using PyTorch DDP
- Configure Kubernetes Jobs for parallel training workloads
- Enable pod-to-pod communication using headless Services
- Manage training data and model checkpoints with PersistentVolumes
- Coordinate multiple training workers using Kubernetes Job parallelism
- Monitor distributed training progress across multiple pods

---

## Table of Contents

- [Prerequisites](#prerequisites)
- [Quick Start / TL;DR](#quick-start--tldr)
- [Detailed Steps & Explanation](#detailed-steps--explanation)
- [Verification / Seeing it Work](#verification--seeing-it-work)
- [Configuration Customization](#configuration-customization)
- [Platform-Specific Configuration](#platform-specific-configuration)
- [Cleanup](#cleanup)
- [Troubleshooting](#troubleshooting)
- [Further Reading / Next Steps](#further-reading--next-steps)

---

## Prerequisites

- A Kubernetes cluster (v1.35+) with access to NVIDIA GPUs.
- NVIDIA device plugin installed in your cluster (for GPU support)
- At least 2 GPU nodes, each with 2+ GPUs (for multi-node training)
- `kubectl` configured to communicate with your cluster
- Sufficient cluster resources:
  - CPU: 4 cores per training pod
  - Memory: 16Gi per training pod
  - GPUs: 2 GPUs per training pod (adjust based on your setup)
  - Storage: 50Gi for training data, 100Gi for outputs/checkpoints

**Note:** This example uses CIFAR-10 dataset which will be downloaded automatically. For production use, you should pre-populate the data volume with your training dataset.

---

## Quick Start / TL;DR
Run single command to apply all config

```bash
kubectl apply -k .
```

## Each step one by one

```bash
# Create namespace
kubectl create namespace pytorch-training

# Apply ConfigMaps
kubectl apply -f training-config.yaml -n pytorch-training
kubectl apply -f training-script-configmap.yaml -n pytorch-training

# Create PersistentVolumeClaims
kubectl apply -f data-pvc.yaml -n pytorch-training
kubectl apply -f output-pvc.yaml -n pytorch-training

# Create headless Service for pod communication
kubectl apply -f service.yaml -n pytorch-training

# Create Workload for workload aware scheduling (v1.35)
kubectl apply -f workload.yaml -n pytorch-training

# Start distributed training job
kubectl apply -f training-job.yaml -n pytorch-training


# Monitor training progress
kubectl logs -f job/pytorch-ddp-training -n pytorch-training
```

---

## Detailed Steps & Explanation

### 1. Create a Namespace

Create a dedicated namespace for the training job:

```bash
kubectl create namespace pytorch-training
```

Using a namespace helps organize resources and simplifies cleanup.

### 2. Create ConfigMaps

ConfigMaps store training configuration and the training script:

```bash
kubectl apply -f training-config.yaml -n pytorch-training
kubectl apply -f training-script-configmap.yaml -n pytorch-training
```

**training-config.yaml**: Contains training hyperparameters (epochs, batch size) that can be easily modified.

**training-script-configmap.yaml**: Contains the PyTorch training script that will be mounted into the training pods.

### 3. Create PersistentVolumeClaims

Create PVCs for training data and outputs:

```bash
kubectl apply -f data-pvc.yaml -n pytorch-training
kubectl apply -f output-pvc.yaml -n pytorch-training
```

- **data-pvc.yaml**: Read-only volume for training data (ReadOnlyMany access mode allows multiple pods to read the same data)
- **output-pvc.yaml**: Read-write volume for model checkpoints and outputs (ReadWriteMany allows multiple pods to write)

**Note:** For local development, you may need to create corresponding PersistentVolumes or use a StorageClass that supports the required access modes.

### 4. Create Headless Service

The headless Service enables pod-to-pod communication for DDP:

```bash
kubectl apply -f service.yaml -n pytorch-training
```

**Why headless Service?** PyTorch DDP requires workers to communicate directly with each other. A headless Service (clusterIP: None) allows pods to discover each other using DNS names like `pytorch-training-headless.pytorch-training.svc.cluster.local`.

### 5. Deploy Training Job

Start the distributed training job:

```bash
kubectl apply -f training-job.yaml -n pytorch-training
```

**Key components of training-job.yaml:**

- **Job with parallelism**: Uses `completions: 2` and `parallelism: 2` to create 2 worker pods
- **Environment variables**: Each pod determines its rank and master address for DDP coordination
- **GPU resources**: Requests 2 GPUs per pod (adjust based on your node configuration)
- **Volume mounts**: Mounts data, output, and training script volumes
- **Subdomain**: Uses `subdomain` field to enable DNS-based pod discovery

### 6. How Distributed Training Works

This example demonstrates PyTorch DDP with the following setup:

1. **Multiple Pods**: Kubernetes Job creates multiple pods (workers), each running the same training script
2. **Rank Assignment**: Each pod extracts its rank from the pod name (e.g., `pytorch-ddp-training-0` → rank 0, `pytorch-ddp-training-1` → rank 1)
3. **Master Discovery**: Pods discover the master address via the headless Service DNS
4. **Process Group**: PyTorch initializes a process group using NCCL backend for GPU communication
5. **Data Sharding**: DistributedSampler ensures each worker processes a different subset of data
6. **Gradient Synchronization**: DDP automatically synchronizes gradients across all workers during backpropagation

---

## Verification / Seeing it Work

### Check Job Status

```bash
kubectl get jobs -n pytorch-training
```

Expected output:
```
NAME                   COMPLETIONS   DURATION   AGE
pytorch-ddp-training   2/2           15m        15m
```

### Check Pod Status

```bash
kubectl get pods -n pytorch-training
```

You should see 2 pods (or more based on your completions setting):
```
NAME                         READY   STATUS      RESTARTS   AGE
pytorch-ddp-training-0       0/1     Completed   0          15m
pytorch-ddp-training-1       0/1     Completed   0          15m
```

### View Training Logs

View logs from a specific pod:

```bash
kubectl logs pytorch-ddp-training-0 -n pytorch-training
```

Or view logs from all pods:

```bash
kubectl logs -l app=pytorch-training -n pytorch-training
```

Expected log output (from rank 0):
```
Running DDP training on rank 0 of 2
Epoch 0, Batch 0, Loss: 2.3026
Epoch 0, Batch 100, Loss: 1.8234
...
Epoch 0 completed. Average Loss: 1.6543
Checkpoint saved to /output/checkpoint_epoch_0.pt
...
Final model saved to /output/final_model.pt
```

### Verify Output Files

Check that checkpoints and model files were created:

```bash
# Get pod name to exec into
POD_NAME=$(kubectl get pods -n pytorch-training -l app=pytorch-training -o jsonpath='{.items[0].metadata.name}')

# List output files
kubectl exec $POD_NAME -n pytorch-training -- ls -lh /output
```

Expected output:
```
total 50M
-rw-r--r-- 1 root root 2.5M checkpoint_epoch_0.pt
-rw-r--r-- 1 root root 2.5M checkpoint_epoch_1.pt
...
-rw-r--r-- 1 root root 2.5M final_model.pt
drwxr-xr-x 2 root root 4.0K tensorboard/
```

### Access TensorBoard Logs (Optional)

If you want to visualize training metrics, you can access TensorBoard logs:

```bash
# Port forward to access TensorBoard (if you install it in a pod)
kubectl port-forward <pod-name> 6006:6006 -n pytorch-training
```

---

## Configuration Customization

### Adjust Number of Workers

To change the number of training workers, modify `training-job.yaml`:

```yaml
spec:
  completions: 4      # Number of worker pods
  parallelism: 4      # Number of pods to run concurrently
```

And update the `WORLD_SIZE` environment variable:

```yaml
- name: WORLD_SIZE
  value: "4"  # Match the number of completions
```

### Change Training Hyperparameters

Edit `training-config.yaml`:

```yaml
data:
  num_epochs: "20"    # Increase training epochs
  batch_size: "64"    # Increase batch size per GPU
```

### Modify GPU Resources

Adjust GPU requests/limits in `training-job.yaml`:

```yaml
resources:
  requests:
    nvidia.com/gpu: "1"  # Use 1 GPU per pod
  limits:
    nvidia.com/gpu: "1"
```

**Note:** Ensure your nodes have sufficient GPUs. For 4 workers with 2 GPUs each, you need nodes with at least 2 GPUs.

### Use Your Own Dataset

1. Create a PersistentVolume with your dataset
2. Update the data PVC to use that volume
3. Modify the training script to load your dataset instead of CIFAR-10

### Custom Training Script

Replace the script in `training-script-configmap.yaml` with your own PyTorch training script. Ensure it:

- Accepts `--rank`, `--world-size`, `--master-addr`, `--master-port` arguments
- Uses `DistributedSampler` for data sharding
- Wraps the model with `DistributedDataParallel`
- Saves checkpoints to `/output`

---

## Platform-Specific Configuration

### Cloud Provider Configurations

#### GKE (Google Kubernetes Engine) - Cloud-specific

Uncomment the GKE nodeSelector in `training-job.yaml`:

```yaml
nodeSelector:
  cloud.google.com/gke-accelerator: nvidia-tesla-v100
  cloud.google.com/gke-gpu-driver-version: default
```

Available GPU types: `nvidia-tesla-v100`, `nvidia-tesla-t4`, `nvidia-tesla-a100`, etc.

**Storage Classes for GKE:**
- For ReadWriteMany: `standard-rwx` or `premium-rwx`
- For ReadOnlyMany: `standard-rwo` or `premium-rwo`

#### EKS (Amazon Elastic Kubernetes Service) - Cloud-specific

Uncomment the EKS nodeSelector:

```yaml
nodeSelector:
  node.kubernetes.io/instance-type: p3.2xlarge
```

Available instance types: `p3.2xlarge`, `p3.8xlarge`, `p4d.24xlarge`, etc.

**Storage Classes for EKS:**
- For ReadWriteMany: Use EFS CSI driver (`efs-sc`) or gp3 with ReadWriteMany support
- For ReadOnlyMany: `gp3` or `gp2`

#### AKS (Azure Kubernetes Service) - Cloud-specific

Uncomment the AKS nodeSelector:

```yaml
nodeSelector:
  agentpiscasi.com/gpu: "true"
```

Or use Azure-specific labels if configured in your cluster.

**Storage Classes for AKS:**
- For ReadWriteMany: `azurefile-csi` (Azure Files)
- For ReadOnlyMany: `managed-premium` or `managed-standard`

### On-Premises / Generic Kubernetes

For on-premises or generic Kubernetes clusters:

1. **Node Selection**: Label your GPU nodes and use nodeSelector:
   ```bash
   # Label your GPU nodes
   kubectl label nodes <node-name> accelerator=nvidia-gpu
   ```
   
   Then uncomment and modify the nodeSelector in `training-job.yaml`:
   ```yaml
   nodeSelector:
     accelerator: nvidia-gpu  # Match your node labels
   ```

2. **Storage Classes**: Use storage backends that support the required access modes:
   - **ReadWriteMany**: NFS, CephFS, GlusterFS
   - **ReadOnlyMany**: NFS, CephFS (read-only mounts)
   
   Example storage classes:
   - NFS: `nfs-client`, `nfs-subdir-external-provisioner`
   - CephFS: `cephfs-csi`
   - GlusterFS: `glusterfs`
   
   Update the `storageClassName` in `data-pvc.yaml` and `output-pvc.yaml` accordingly.

3. **GPU Device Plugin**: Ensure the NVIDIA device plugin is installed:
   ```bash
   # Using NVIDIA GPU Operator (recommended)
   kubectl apply -f https://raw.githubusercontent.com/NVIDIA/gpu-operator/main/deployments/static/gpu-operator.yaml
   
   # Or using DaemonSet directly
   kubectl apply -f https://raw.githubusercontent.com/NVIDIA/k8s-device-plugin/v0.14.1/nvidia-device-plugin.yml
   ```

4. **Network Configuration**: Ensure pod-to-pod communication is enabled (usually default in Kubernetes).

---

## Cleanup

This section provides multiple methods to clean up resources created by this example. Choose the method that best fits your needs.

### ⚠️ Important: Backup Before Cleanup

**Before deleting any resources, ensure you've backed up important data:**

```bash
# Backup model checkpoints and outputs
POD_NAME=$(kubectl get pods -n pytorch-training -l app=pytorch-training -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ ! -z "$POD_NAME" ]; then
  kubectl cp pytorch-training/$POD_NAME:/output ./training-output-backup
  echo "Backup completed to ./training-output-backup"
fi
```

Or use your storage system's backup mechanism if using cloud storage.

---

### Method 1: Delete Individual Resources (Recommended for Selective Cleanup)

This method allows you to delete resources one by one, giving you control over what to keep.

#### Step 1: Stop and Delete the Training Job

```bash
# Delete the training job (this will terminate all training pods)
kubectl delete job pytorch-ddp-training -n pytorch-training

# Wait for pods to terminate
kubectl wait --for=delete pod -l app=pytorch-training -n pytorch-training --timeout=300s

# Verify job is deleted
kubectl get jobs -n pytorch-training
```

#### Step 2: Delete Services

```bash
# Delete the headless service
kubectl delete -f service.yaml -n pytorch-training

# Verify service is deleted
kubectl get svc -n pytorch-training
```

#### Step 3: Delete ConfigMaps

```bash
# Delete training configuration
kubectl delete -f training-config.yaml -n pytorch-training

# Delete training script ConfigMap
kubectl delete -f training-script-configmap.yaml -n pytorch-training

# Verify ConfigMaps are deleted
kubectl get configmaps -n pytorch-training
```

#### Step 4: Delete PersistentVolumeClaims (⚠️ This Deletes Data!)

**Warning:** This will permanently delete all training data and model checkpoints.

```bash
# Delete output PVC (contains checkpoints and final model)
kubectl delete -f output-pvc.yaml -n pytorch-training

# Delete data PVC (contains training dataset)
kubectl delete -f data-pvc.yaml -n pytorch-training

# Verify PVCs are deleted
kubectl get pvc -n pytorch-training
```

**Note:** If you want to keep the data but remove the training job, skip this step.

#### Step 5: Delete Namespace (Optional)

If you want to remove everything including the namespace:

```bash
# Delete the entire namespace (removes all resources within it)
kubectl delete namespace pytorch-training

# Verify namespace is deleted
kubectl get namespace pytorch-training
```

---

### Method 2: Delete All Resources Using Labels

If all resources share the same label, you can delete them all at once:

```bash
# Delete all resources with the app=pytorch-training label
kubectl delete all,configmap,service,pvc,job -l app=pytorch-training -n pytorch-training

# Verify all resources are deleted
kubectl get all,configmap,service,pvc,job -n pytorch-training
```

---

### Method 3: Delete Everything via Namespace (Fastest)

The fastest way to delete everything is to delete the entire namespace:

```bash
# Delete namespace (this removes ALL resources in the namespace)
kubectl delete namespace pytorch-training

# Verify namespace is deleted
kubectl get namespace pytorch-training
```

**Note:** This method will delete:
- All pods, jobs, services, ConfigMaps
- All PVCs and their associated data (unless using Retain policy)
- The namespace itself

---

### Method 4: Cleanup Script

For convenience, you can create a cleanup script:

```bash
#!/bin/bash
# cleanup.sh - Cleanup script for PyTorch distributed training example

NAMESPACE="pytorch-training"

echo "Starting cleanup of PyTorch training resources..."

# Delete Job
echo "Deleting training job..."
kubectl delete job pytorch-ddp-training -n $NAMESPACE 2>/dev/null

# Wait for pods to terminate
echo "Waiting for pods to terminate..."
kubectl wait --for=delete pod -l app=pytorch-training -n $NAMESPACE --timeout=300s 2>/dev/null || true

# Delete Services
echo "Deleting services..."
kubectl delete -f service.yaml -n $NAMESPACE 2>/dev/null || true

# Delete ConfigMaps
echo "Deleting ConfigMaps..."
kubectl delete -f training-config.yaml -n $NAMESPACE 2>/dev/null || true
kubectl delete -f training-script-configmap.yaml -n $NAMESPACE 2>/dev/null || true

# Prompt before deleting PVCs
read -p "Delete PersistentVolumeClaims (this will delete all data)? [y/N]: " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
  echo "Deleting PVCs..."
  kubectl delete -f output-pvc.yaml -n $NAMESPACE 2>/dev/null || true
  kubectl delete -f data-pvc.yaml -n $NAMESPACE 2>/dev/null || true
else
  echo "Skipping PVC deletion. Data preserved."
fi

# Prompt before deleting namespace
read -p "Delete namespace '$NAMESPACE'? [y/N]: " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
  echo "Deleting namespace..."
  kubectl delete namespace $NAMESPACE 2>/dev/null || true
else
  echo "Keeping namespace. Cleanup complete."
fi

echo "Cleanup finished!"
```

Save this as `cleanup.sh`, make it executable, and run:

```bash
chmod +x cleanup.sh
./cleanup.sh
```

---

### Verification After Cleanup

After cleanup, verify all resources are removed:

```bash
# Check for remaining resources in the namespace
kubectl get all,configmap,service,pvc,job -n pytorch-training

# If namespace still exists, check its status
kubectl get namespace pytorch-training

# List all resources (should be empty or show only system resources)
kubectl get all -n pytorch-training
```

---

### Troubleshooting Cleanup Issues

#### PVCs Won't Delete (Stuck in "Terminating")

If PVCs are stuck in "Terminating" state, they may be in use or have a finalizer:

```bash
# Check PVC status
kubectl get pvc -n pytorch-training

# Check if any pods are using the PVC
kubectl get pods -n pytorch-training -o json | jq '.items[] | select(.spec.volumes[].persistentVolumeClaim.claimName)'

# Force delete PVC (use with caution)
kubectl patch pvc <pvc-name> -n pytorch-training -p '{"metadata":{"finalizers":null}}'
```

#### Job Won't Delete

If the Job is stuck:

```bash
# Check Job status
kubectl describe job pytorch-ddp-training -n pytorch-training

# Force delete pods if needed
kubectl delete pods -l app=pytorch-training -n pytorch-training --force --grace-period=0

# Then delete the job
kubectl delete job pytorch-ddp-training -n pytorch-training
```

#### Namespace Stuck in "Terminating"

If namespace won't delete:

```bash
# Check what's preventing deletion
kubectl get all -n pytorch-training

# Remove finalizers (use with extreme caution)
kubectl get namespace pytorch-training -o json | \
  jq '.spec.finalizers = []' | \
  kubectl replace --raw /api/v1/namespaces/pytorch-training/finalize -f -
```

---

### Quick Reference: One-Line Cleanup Commands

**Delete everything (including data):**
```bash
kubectl delete namespace pytorch-training
```

**Delete job and services only (keep data):**
```bash
kubectl delete job pytorch-ddp-training -n pytorch-training && kubectl delete -f service.yaml -n pytorch-training
```

**Delete all resources except PVCs:**
```bash
kubectl delete all,configmap,service,job -l app=pytorch-training -n pytorch-training
```

---

## Troubleshooting

### Pods Stuck in Pending State

**Issue**: Pods cannot be scheduled.

**Possible causes:**
- Insufficient GPU resources in the cluster
- NodeSelector doesn't match any nodes
- PVCs cannot be bound (check StorageClass availability)

**Solution:**
```bash
# Check pod events
kubectl describe pod <pod-name> -n pytorch-training

# Check node resources
kubectl describe nodes | grep -A 5 "Allocated resources"

# Check PVC status
kubectl get pvc -n pytorch-training
```

### Training Fails with "Connection Refused"

**Issue**: Workers cannot communicate with the master.

**Possible causes:**
- Headless Service not created
- DNS resolution issues
- Network policies blocking pod-to-pod communication

**Solution:**
```bash
# Verify Service exists
kubectl get svc pytorch-training-headless -n pytorch-training

# Test DNS resolution from a pod
kubectl run -it --rm debug --image=busybox --restart=Never -n pytorch-training -- nslookup pytorch-training-headless.pytorch-training.svc.cluster.local
```

### CUDA Out of Memory Errors

**Issue**: Training fails with OOM errors.

**Solution:**
- Reduce batch size in `training-config.yaml`
- Use fewer GPUs per pod
- Use gradient accumulation in your training script
- Use a smaller model

### Checkpoints Not Saving

**Issue**: Output directory is empty or read-only.

**Solution:**
```bash
# Check PVC status
kubectl get pvc training-output-pvc -n pytorch-training

# Check pod volume mounts
kubectl describe pod <pod-name> -n pytorch-training | grep -A 5 "Mounts"

# Verify write permissions
kubectl exec <pod-name> -n pytorch-training -- touch /output/test.txt
```

### Uneven Training Speed

**Issue**: Workers finish at different times.

**Possible causes:**
- Data loading bottlenecks
- Network communication delays
- Different GPU performance

**Solution:**
- Ensure `DistributedSampler` is used correctly
- Increase `num_workers` in DataLoader
- Use faster storage (SSD) for datasets
- Monitor network bandwidth between nodes

---

## Further Reading / Next Steps

- [PyTorch Distributed Data Parallel Documentation](https://pytorch.org/tutorials/intermediate/ddp_tutorial.html)
- [Kubernetes Jobs Documentation](https://kubernetes.io/docs/concepts/workloads/controllers/job/)
- [Kubernetes PersistentVolumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/)
- [Headless Services in Kubernetes](https://kubernetes.io/docs/concepts/services-networking/service/#headless-services)
- [NVIDIA GPU Operator](https://github.com/NVIDIA/gpu-operator) - For GPU device plugin setup
- [PyTorch Lightning](https://lightning.ai/docs/pytorch/stable/) - Higher-level framework that simplifies distributed training
- [Horovod with PyTorch](https://github.com/horovod/horovod) - Alternative distributed training framework
- [Kubeflow Training Operator](https://www.kubeflow.org/docs/components/training/) - Kubernetes-native training operators

---

## Key Kubernetes Concepts Demonstrated

This example showcases several important Kubernetes concepts:

1. **Jobs**: For running batch workloads that complete successfully
2. **Job Parallelism**: Running multiple pod replicas for distributed workloads
3. **Headless Services**: Enabling direct pod-to-pod communication via DNS
4. **PersistentVolumeClaims**: Managing storage for data and outputs
5. **ConfigMaps**: Storing configuration and scripts
6. **Resource Requests/Limits**: Managing GPU and CPU allocation
7. **Node Selection**: Targeting specific node types (GPU nodes)
8. **Environment Variables**: Passing configuration to containers
9. **Volume Mounts**: Sharing data between pods and persistent storage

---

## Last Validated Kubernetes Version

Kubernetes v1.35 for workload aware scheduling (or v1.28+ if you skip workload.yaml)

---

*For questions or issues, please refer to the [Kubernetes Examples repository](https://github.com/kubernetes/examples) or open an issue.*

