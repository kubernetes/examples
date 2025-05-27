
To expose this externally, ensure you have an Ingress Controller installed (e.g., NGINX).

Adjust the host value in ingress.yaml to match your domain or use a LoadBalancer for direct access instead of Ingress.

For basic testing:
```
curl -X POST http://<ingress-host>/tf/v1/models/my_model:predict -d '{ "instances": [[1.0, 2.0, 5.0]] }'
```

PVC 

⚠️ Note: You must create a PersistentVolumeClaim named my-model-pvc that points to the directory where your SavedModel is stored (/models/my_model/1/saved_model.pb).

📌 For demo/testing purposes, this uses a hostPath volume — useful for single-node clusters (like Minikube or KIND).
Replace hostPath with a cloud volume (e.g., GCP PD, AWS EBS, NFS, etc.) in production environments.

 Directory Structure on Node (/mnt/models/my_model/)
Your model directory (mounted on the node at /mnt/models/my_model/) should look like this:

```
/mnt/models/my_model/
└── 1/
    ├── saved_model.pb
    └── variables/
```

✅ How to Load a Demo Model (if using local clusters)
```
mkdir -p /mnt/models/my_model/1
wget https://storage.googleapis.com/tf-serving-models/resnet_v2.tar.gz
tar -xzvf resnet_v2.tar.gz -C /mnt/models/my_model/1 --strip-components=1
```


