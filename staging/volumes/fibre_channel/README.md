# Consuming Fibre Channel Storage on Kubernetes

## Table of Contents

- [Example Parameters](#example-parameters)
- [Step-by-Step](#step-by-step)
- [Multipath Considerations](#multipath-considerations)

## Example Parameters

```yaml
 fc:
   targetWWNs:
     - '500a0982991b8dc5'
     - '500a0982891b8dc5'
   lun: 2
   fsType: ext4
   readOnly: true
```

[API Reference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#fcvolumesource-v1-core)

## Step-by-Step

### Set up a Fibre Channel Target

Using your Fibre Channel SAN Zone manager you must allocate and mask LUNs so that all hosts in the Kubernetes cluster can access them

### Prepare nodes in your Kubernetes cluster

You will need to install and configured a Fibre Channel initiator on the hosts within your Kubernetes cluster.

### Create a Pod using Fibre Channel persistent storage

Create a pod manifest based on  [fc.yaml](fc.yaml). You will need to provide *targetWWNs* (array of Fibre Channel target's World Wide Names), *lun*, and the type of the filesystem that has been created on the LUN if it is not _ext4_

Once you have created a pod manifest you can deploy it by running:

```console
kubectl apply -f ./your_new_pod.yaml
```

You can then confirm that the pod hase been sucessfully deployed by running `kubectl get pod fibre-channel-example-pod -o wide`

```console
# kubectl get pod fibre-channel-example-pod -o wide
NAME                        READY   STATUS          RESTARTS   AGE    IP               NODE    NOMINATED NODE   READINESS GATES
fibre-channel-example-pod   1/1     READY           0          1m8s   192.168.172.11   node0   <none>           <none>

```

If you connect to the console on the Kubernetes node that the pod has been assigned to you can see that the volume is mounted to the pod by running `mount | grep /var/lib/kubelet/plugins/kubernetes.io/fc/`

```console
# mount | grep /var/lib/kubelet/plugins/kubernetes.io/fc/
/dev/mapper/360a98000324669436c2b45666c567946 on /var/lib/kubelet/plugins/kubernetes.io/fc/500a0982991b8dc5-lun-2 type ext4 (relatime,seclabel,stripe=16,data=ordered)
  ```

## Multipath Considerations

To leverage multiple paths for block storage, it is important to perform
multipath configuration on the host.
If your distribution does not provide `/etc/multipath.conf`, then you can
either use the following minimalistic one:

```
defaults {
    find_multipaths yes
    user_friendly_names yes
}
```

or create a new one by running:

```console
$ mpathconf --enable
```

Finally you'll need to ensure to start or reload and enable multipath:

```console
$ systemctl enable --now multipathd.service
```

**Note:** Any change to `multipath.conf` or enabling multipath can lead to
inaccessible block devices as they will be claimed by multipath and
exposed as a device in /dev/mapper/*.


<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/volumes/fibre_channel/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
