# How to Use it?

Install *cifs-utils* on the Kubernetes host. For example, on Fedora based Linux

    # yum -y install cifs-utils

Note, as explained in [Azure File Storage for Linux](https://azure.microsoft.com/en-us/documentation/articles/storage-how-to-use-files-linux/), the Linux hosts and the file share must be in the same Azure region.

## Create a storage access secret

Obtain an Microsoft Azure storage account and extract the storage account name (which you provided) and one of the storage account keys. You will then need to create a Kubernetes secret which holds both the account name and key. You can use `kubectl` directly to create the secret:

```console
# kubectl create secret generic azure-secret --from-literal=azurestorageaccountname=<...> --from-literal=azurestorageaccountkey=<...>
```

Alternatively, you can create a [secret](secret/azure-secret.yaml) that contains the base64 encoded Azure Storage account name and key. In the secret file, base64-encode Azure Storage account name and pair it with name `azurestorageaccountname`, and base64-encode Azure Storage access key and pair it with name `azurestorageaccountkey`. The advantage of this is that you can `kubectl apply -f` the secret file, whereas you need to delete a secret before you can create a new one using `kubectl create secret`.

Based on the storage account name, and using the [`az` command line](https://docs.microsoft.com/en-us/cli/azure/?view=azure-cli-latest), you can also extract the storage account key using the following command line, given that you are logged in using `az login` with a service principal which has access to the service account:

```console
# export STORAGE_ACCOUNT_KEY=$(az storage account keys list -n <storage account name> -g <resource group> --query='[0].value' | tr -d '"')
```

## Pod creation

Then create a Pod using the volume spec based on [azure](azure.yaml).

In the pod, you need to provide the following information:

- `secretName`:  the name of the secret that contains both Azure storage account name and key.
- `shareName`: The share name to be used.
- `readOnly`: Whether the filesystem is used as readOnly.
- `secretNamespace`: (optional) The namespace in which the secret was created; `default` is used if not set

Create the secret:

```console
    # kubectl create -f examples/volumes/azure_file/secret/azure-secret.yaml
```

You should see the account name and key from `kubectl get secret`

### Mount volume directly in Pod

Then create the Pod:

```console
    # kubectl create -f examples/volumes/azure_file/azure.yaml
```

### Mount volume via `pv` and `pvc`

The same mechanism can also be used to mount the Azure File Storage using a Persistent Volume and a Persistent Volume Claim:

* [Persistent Volume using `azureFile`](azure-pv.yaml)
* [Persistent Volume Claim matching the Volume](azure-pvc.yaml)

Correspondingly, you then mount the volume inside pods using the normal `persistentVolumeClaim` reference. This mechanism is used in the sample pod YAML [azure-2.yaml](azure-2.yaml).

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/volumes/azure_file/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
