# How to Use it?

On Azure VM, create a Pod using the volume spec based on [azure](azure.yaml).

In the pod, you need to provide the following information:

- *diskName*:  (required) the name of the VHD blob object OR the name of an Azure managed data disk if Kind is Managed.
- *diskURI*: (required) the URI of the vhd blob object OR the resourceID of an Azure managed data disk if Kind is Managed.
- *kind*: (optional) kind of disk. Must be one of Shared (multiple disks per storage account), Dedicated (single blob disk per storage account), or Managed (Azure managed data disk). Default is Shared.
- *cachingMode*: (optional) disk caching mode. Must be one of None, ReadOnly, or ReadWrite. Default is None.
- *fsType*:  (optional) the filesystem type to mount. Default is ext4.
- *readOnly*: (optional) whether the filesystem is used as readOnly. Default is false.


Launch the Pod:

```console
    # kubectl create -f examples/volumes/azure_disk/azure.yaml
```

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/volumes/azure_disk/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
