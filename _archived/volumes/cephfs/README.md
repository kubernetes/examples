# How to Use it?

NOTE: CephFS storage driver ( `kubernetes.io/cephfs`) has been
deprecated in kubernetes 1.28 release and will be in removed in subsequent
releases. The CSI driver for CephFS (https://github.com/ceph/ceph-csi)
is available for long time now which can be an alternative option for the
users want to use CephFS volumes in their clusters.

Install Ceph on the Kubernetes host. For example, on Fedora 21

    # yum -y install ceph

If you don't have a Ceph cluster, you can set up a [containerized Ceph cluster](https://github.com/ceph/ceph-docker/tree/master/examples/kubernetes)

Then get the keyring from the Ceph cluster and copy it to */etc/ceph/keyring*.

Once you have installed Ceph and a Kubernetes cluster, you can create a pod based on my examples [cephfs.yaml](cephfs.yaml)  and [cephfs-with-secret.yaml](cephfs-with-secret.yaml). In the pod yaml, you need to provide the following information.

- *monitors*:  Array of Ceph monitors.
- *path*: Used as the mounted root, rather than the full Ceph tree. If not provided, default */* is used.
- *user*: The RADOS user name. If not provided, default *admin* is used.
- *secretFile*: The path to the keyring file. If not provided, default */etc/ceph/user.secret* is used.
- *secretRef*: Reference to Ceph authentication secrets. If provided, *secret* overrides *secretFile*.
- *readOnly*: Whether the filesystem is used as readOnly.


Here are the commands:

```console
    # kubectl create -f examples/volumes/cephfs/cephfs.yaml

    # create a secret if you want to use Ceph secret instead of secret file
    # kubectl create -f examples/volumes/cephfs/secret/ceph-secret.yaml
	
    # kubectl create -f examples/volumes/cephfs/cephfs-with-secret.yaml
    # kubectl get pods
```

 If you ssh to that machine, you can run `docker ps` to see the actual pod and `docker inspect` to see the volumes used by the container.

