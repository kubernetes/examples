# Dell EMC ScaleIO Volume Plugin for Kubernetes

This document shows how to configure Kubernetes resources to consume storage from volumes hosted on ScaleIO cluster.

## Pre-Requisites

* Kubernetes ver 1.6 or later
* ScaleIO ver 2.0 or later
* A ScaleIO cluster with an API gateway
* ScaleIO SDC binary installed/configured on each Kubernetes node that will consume storage

## ScaleIO Setup

This document assumes you are familiar with ScaleIO and have a cluster ready to go.  If you are *not familiar* with ScaleIO, please review *Learn how to setup a 3-node* [ScaleIO cluster on Vagrant](https://github.com/codedellemc/labs/tree/master/setup-scaleio-vagrant) and see *General instructions on* [setting up ScaleIO](https://www.emc.com/products-solutions/trial-software-download/scaleio.htm)

For this demonstration, ensure the following: 

 - The ScaleIO `SDC` component is installed and properly configured on all Kubernetes nodes where deployed pods will consume ScaleIO-backed storage.
 - You have a configured ScaleIO gateway that is accessible from the Kubernetes nodes. 

## Deploy Kubernetes Secret for ScaleIO

The ScaleIO plugin uses a Kubernetes Secret object to store the `username` and `password` credentials.  Kubernetes requires the secret values to be base64-encoded to simply obfuscate (not encrypt) the clear text as shown below.

```
$> echo -n "siouser" | base64
c2lvdXNlcg==
$> echo -n "sc@l3I0" | base64
c2NAbDNJMA==
```
The previous will generate `base64-encoded` values for the username and password.  
Remember to generate the credentials for your own environment and copy them in a secret file similar to the following.  

File: [secret.yaml](secret.yaml)

```
apiVersion: v1
kind: Secret
metadata:
  name: sio-secret
type: kubernetes.io/scaleio
data:
  username: c2lvdXNlcg==
  password: c2NAbDNJMA==
```

Notice the name of the secret specified above as `sio-secret`.  It will be referred in other YAML configuration files later.  Next, deploy the secret.

```
$ kubectl create -f ./examples/volumes/scaleio/secret.yaml
```
Read more about Kubernetes secrets [here](https://kubernetes.io/docs/concepts/configuration/secret/).

## Deploying Pods with Persistent Volumes

The example presented in this section shows how the ScaleIO volume plugin can automatically attach, format, and mount an existing ScaleIO volume for pod.  The Kubernetes ScaleIO volume spec supports the following attributes:

| Attribute | Description |
|-----------|-------------|
| gateway | address to a ScaleIO API gateway (required)|
| system  | the name of the ScaleIO system (required)|
| protectionDomain| the name of the ScaleIO protection domain (required)|
| storagePool| the name of the volume storage pool (required)|
| storageMode| the storage provision mode: `ThinProvisioned` (default) or `ThickProvisioned`|
| volumeName| the name of an existing volume in ScaleIO (required)|
| secretRef:name| references the name of a Secret object (required)|
| readOnly| specifies the access mode to the mounted volume (default `false`)|
| fsType| the file system to use for the volume (default `ext4`)|

### Create Volume

When using static persistent volumes, it is required that the volume, to be consumed by the pod, be already created in ScaleIO.  For this demo, we assume there's an existing ScaleIO volume named `vol-0` which is reflected configuration properly `volumeName:`  below.

### Deploy Pod YAML

Create a pod YAML file that declares the volume (above) to be used.

File: [pod.yaml](pod.yaml)

```
apiVersion: v1
kind: Pod
metadata:
  name: pod-0
spec:
  containers:
  - image: k8s.gcr.io/test-webserver
    name: pod-0
    volumeMounts:
    - mountPath: /test-pd
      name: vol-0
  volumes:
  - name: vol-0
    scaleIO:
      gateway: https://localhost:443/api
      system: scaleio
      protectionDomain: pd01
      storagePool: sp01
      volumeName: vol-0
      secretRef:
        name: sio-secret
      fsType: xfs
```
Remember to change the ScaleIO attributes above to reflect that of your own environment.

Next, deploy the pod.

```
$> kubectl create -f examples/volumes/scaleio/pod.yaml
```
You can verify the pod:
```
$> kubectl get pod
NAME      READY     STATUS    RESTARTS   AGE
pod-0     1/1       Running   0          33s
```
Or for more detail, use 
```
kubectl describe pod pod-0
```
You can see the attached/mapped volume on the node:
```
$> lsblk
NAME        MAJ:MIN RM  SIZE RO TYPE MOUNTPOINT
...
scinia      252:0    0    8G  0 disk /var/lib/kubelet/pods/135986c7-dcb7-11e6-9fbf-080027c990a7/volumes/kubernetes.io~scaleio/vol-0
```

## StorageClass and Dynamic Provisioning

The ScaleIO volume plugin can also dynamically provision storage to a Kubernetes cluster. 
The ScaleIO dynamic provisioner plugin can be used with a `StorageClass` and is identified as `kubernetes.io/scaleio`.  

### ScaleIO StorageClass
The ScaleIO dynamic provisioning plugin supports the following StorageClass parameters:

| Parameter | Description |
|-----------|-------------|
| gateway | address to a ScaleIO API gateway (required)|
| system  | the name of the ScaleIO system (required)|
| protectionDomain| the name of the ScaleIO protection domain (required)|
| storagePool| the name of the volume storage pool (required)|
| storageMode| the storage provision mode: `ThinProvisioned` (default) or `ThickProvisioned`|
| secretRef| reference to the name of a configuered Secret object (required)|
| readOnly| specifies the access mode to the mounted volume (default `false`)|
| fsType| the file system to use for the volume (default `ext4`)|

The following shows an example of ScaleIO  `StorageClass` configuration YAML:

File [sc.yaml](sc.yaml)

```
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: sio-small
provisioner: kubernetes.io/scaleio
parameters:
  gateway: https://localhost:443/api
  system: scaleio
  protectionDomain: pd01
  storagePool: sp01
  secretRef: sio-secret
  fsType: xfs
```
Note the `metadata:name` attribute of the StorageClass is set to `sio-small` and will be referenced later.  Again, remember to update other parameters to reflect your environment setup.

Next, deploy the storage class file.

```
$> kubectl create -f examples/volumes/scaleio/sc.yaml

$> kubectl get sc
NAME        TYPE
sio-small   kubernetes.io/scaleio
```

### PVC for the StorageClass

The next step is to define/deploy a `PersistentVolumeClaim` that will use the StorageClass.

File [sc-pvc.yaml](sc-pvc.yaml)

```
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: pvc-sio-small
spec:
  storageClassName: sio-small
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
```

Note the `spec:storageClassName` entry which specifies the name of the previously defined StorageClass `sio-small` .

Next, deploy the PVC file.  This step will cause the Kubernetes ScaleIO plugin to create the volume in the storage system.  
```
$> kubectl create -f examples/volumes/scaleio/sc-pvc.yaml
```
You verify that a new volume created in the ScaleIO dashboard.  You can also verify the newly created volume as follows.
```
 kubectl get pvc
NAME            STATUS    VOLUME                CAPACITY   ACCESSMODES   AGE
pvc-sio-small   Bound     k8svol-5fc78518dcae   10Gi       RWO           1h
```

###Pod for PVC and SC
At this point, the volume is created (by the claim) in the storage system.  To use it, we must define a pod that references the volume as done in this YAML.

File [pod-sc-pvc.yaml](pod-sc-pvc.yaml)

```
kind: Pod
apiVersion: v1
metadata:
  name: pod-sio-small
spec:
  containers:
    - name: pod-sio-small-container
      image: k8s.gcr.io/test-webserver
      volumeMounts:
      - mountPath: /test
        name: test-data
  volumes:
    - name: test-data
      persistentVolumeClaim:
        claimName: pvc-sio-small
```

Notice that the `claimName:` attribute refers to the name of the PVC, `pvc-sio-small`, defined and deployed earlier.  Next, let us deploy the file.

```
$> kubectl create -f examples/volumes/scaleio/pod-sc-pvc.yaml
```
We can now verify that the new pod is deployed OK.
```
kubectl get pod
NAME            READY     STATUS    RESTARTS   AGE
pod-0           1/1       Running   0          23m
pod-sio-small   1/1       Running   0          5s
``` 
<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/volumes/scaleio/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
