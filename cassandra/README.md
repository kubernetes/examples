<!-- EXCLUDE_FROM_DOCS BEGIN -->

> :warning: :warning: Follow this tutorial on the Kubernetes website:
> https://kubernetes.io/docs/tutorials/stateful-application/cassandra/.
> Otherwise some of the URLs will not work properly.

# Cloud Native Deployments of Cassandra using Kubernetes
<!-- EXCLUDE_FROM_DOCS END -->

{% capture overview %}
This tutorial shows you how to develop a native cloud [Cassandra](http://cassandra.apache.org/) deployment on Kubernetes. In this instance, a custom Cassandra `SeedProvider` enables Cassandra to discover new Cassandra nodes as they join the cluster.

Deploying stateful distributed applications, like Cassandra, within a clustered environment can be challenging. StatefulSets greatly simplify this process. Please read about [StatefulSets](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/)  for more information about the features used in this tutorial.

**Cassandra Docker**
The pods use the [```gcr.io/google-samples/cassandra:v12```](https://github.com/kubernetes/examples/blob/master/cassandra/image/Dockerfile)
image from Google's [container registry](https://cloud.google.com/container-registry/docs/).
The docker is based on `debian:jessie` and includes OpenJDK 8. This image includes a standard Cassandra installation from the Apache Debian repo.  By using environment variables you can change values that are inserted into `cassandra.yaml`.

| ENV VAR       | DEFAULT VALUE  |
| ------------- |:-------------: |
| CASSANDRA_CLUSTER_NAME | 'Test Cluster'  |
| CASSANDRA_NUM_TOKENS  | 32               |
| CASSANDRA_RPC_ADDRESS | 0.0.0.0          |

{% endcapture %}

{% capture objectives %}
* Create and Validate a Cassandra headless `Service`.
* Use a `StatefulSet` to create a Cassandra ring.
* Validate the `StatefulSet`.
* Modify the `StatefulSet`.
* Delete the `StatefulSet` and its `Pods`.
{% endcapture %}

{% capture prerequisites %}
To complete this tutorial, you should already have a basic familiarity with [Pods](https://kubernetes.io/docs/docs/concepts/workloads/pods/pod/), [Services](https://kubernetes.io/docs/docs/concepts/services-networking/service/), and [StatefulSets](https://kubernetes.io/docs/docs/concepts/workloads/controllers/statefulset/). In addition, you should:

* [Install and Configure](https://kubernetes.io/docs/docs/tasks/tools/install-kubectl/) the `kubectl` command line

* Download [cassandra-service.yaml](https://kubernetes.io/docs/docs/tutorials/stateful-application/cassandra-service.yaml) and [cassandra-statefulset.yaml](https://kubernetes.io/docs/docs/tutorials/stateful-application/cassandra-statefulset.yaml)

* Have a supported Kubernetes Cluster running

**Note:** Please read the [getting started guides](https://kubernetes.io/docs/docs/setup/pick-right-solution/) if you do not already have a cluster. 

### Additional Minikube Setup Instructions

**Warning:** [Minikube](https://kubernetes.io/docs/docs/getting-started-guides/minikube/) defaults to 1024MB of memory and 1 CPU which results in an insufficient resource errors. 

To avoid these errors, run minikube with:

```
minikube start --memory 5120 --cpus=4
``` 
{% endcapture %}

{% capture lessoncontent %}
## Creating a Cassandra Headless Service
A Kubernetes [Service](/docs/concepts/services-networking/service/) describes a set of [Pods](https://kubernetes.io/docs/docs/concepts/workloads/pods/pod/) that perform the same task. 

The following `Service` is used for DNS lookups between Cassandra pods and clients within the Kubernetes Cluster.

1. `cd` to the folder you saved the .yaml files.
2. Create a `Service` to track all Cassandra StatefulSet Nodes from the following `.yaml` file:

```shell
kubectl create -f cassandra-service.yaml
```

{% include code.html language="yaml" file="cassandra-service.yaml" ghlink="/docs/tutorials/stateful-application/cassandra-service.yaml" %}

### Validating (optional)

Get the Cassandra `Service`.

```shell
kubectl get svc cassandra
```

The response should be

```console
NAME        CLUSTER-IP   EXTERNAL-IP   PORT(S)    AGE
cassandra   None         <none>        9042/TCP   45s
```

If anything else returns, the service was not successfully created. Read [Debug Services](https://kubernetes.io/docs/docs/tasks/debug-application-cluster/debug-service/) for common issues.

## Using a StatefulSet to Create a Cassandra Ring

The StatefulSet manifest, included below, creates a Cassandra ring that consists of three pods.

**Note:** This example uses the default provisioner for Minikube. Please update the following StatefulSet for the cloud you are working with. 

1. Update the StatefulSet if necessary.
2. Create the Cassandra StatefulSet from the following `.yaml` file:

```shell
kubectl create -f cassandra-statefulset.yaml
```

{% include code.html language="yaml" file="cassandra-statefulset.yaml" ghlink="/docs/tutorials/stateful-application/cassandra-statefulset.yaml" %}

## Validating The Cassandra StatefulSet

1. Get the Cassandra StatefulSet:

```shell
kubectl get statefulset cassandra
```

   The response should be

```console
NAME        DESIRED   CURRENT   AGE
cassandra   3         0         13s
```

   The StatefulSet resource deploys pods sequentially.  

{:start="2"}
2. Get the Pods to see the ordered creation status:

```shell
kubectl get pods -l="app=cassandra"
NAME          READY     STATUS              RESTARTS   AGE
cassandra-0   1/1       Running             0          1m
cassandra-1   0/1       ContainerCreating   0          8s
```

**Note:** It can take up to ten minutes for all three pods to deploy. 

Once all pods are deployed, the same command returns:

```shell
kubectl get pods -l="app=cassandra"
NAME          READY     STATUS    RESTARTS   AGE
cassandra-0   1/1       Running   0          10m
cassandra-1   1/1       Running   0          9m
cassandra-2   1/1       Running   0          8m
```

Running the Cassandra utility `nodetool` displays the status of the ring.

```
kubectl exec cassandra-0 -- nodetool status
```

```console
Datacenter: DC1-K8Demo
======================
Status=Up/Down
|/ State=Normal/Leaving/Joining/Moving
--  Address   Load       Tokens       Owns (effective)  Host ID                               Rack
UN  10.4.2.4  65.26 KiB  32           63.7%             a9d27f81-6783-461d-8583-87de2589133e  Rack1-K8Demo
UN  10.4.0.4  102.04 KiB  32           66.7%             5559a58c-8b03-47ad-bc32-c621708dc2e4  Rack1-K8Demo
UN  10.4.1.4  83.06 KiB  32           69.6%             9dce943c-581d-4c0e-9543-f519969cc805  Rack1-K8Demo
```
## Modifying the Cassandra StatefulSet
Use `kubectl edit` to modify the size of of a Cassandra StatefulSet. 

1. Run the following command:

```shell
kubectl edit statefulset cassandra
```
   This command opens an editor in your terminal. The line you need to change is `Replicas`.
   **Note:** The following sample is an excerpt of the StatefulSet file.

```console
# Please edit the object below. Lines beginning with a '#' will be ignored,
# and an empty file will abort the edit. If an error occurs while saving this file will be
# reopened with the relevant failures.
#
apiVersion: apps/v1beta1
kind: StatefulSet
metadata:
  creationTimestamp: 2016-08-13T18:40:58Z
  generation: 1
  labels:
    app: cassandra
  name: cassandra
  namespace: default
  resourceVersion: "323"
  selfLink: /apis/apps/v1beta1/namespaces/default/statefulsets/cassandra
  uid: 7a219483-6185-11e6-a910-42010a8a0fc0
spec:
  replicas: 3
```

{:start="2"}
2. Increase the number of replicas to 4, and then save the manifest. 

   The StatefulSet now contains 4 pods.

3. Get the Cassandra StatefulSet to verify:

```shell
kubectl get statefulset cassandra
```

  The response should be

```console
NAME        DESIRED   CURRENT   AGE
cassandra   4         4         36m
```
{% endcapture %}

{% capture cleanup %}
Deleting or scaling a StatefulSet down does not delete the volumes associated with the StatefulSet. This ensures safety first: your data is more valuable than an auto purge of all related StatefulSet resources. 

**Warning:** Depending on the storage class and reclaim policy, deleting the Persistent Volume Claims may cause the associated volumes to also be deleted. Never assume you’ll be able to access data if its volume claims are deleted. 

1. Run the following commands to delete everything in a `StatefulSet`:

```shell
grace=$(kubectl get po cassandra-0 -o=jsonpath='{.spec.terminationGracePeriodSeconds}') \
  && kubectl delete statefulset -l app=cassandra \
  && echo "Sleeping $grace" \
  && sleep $grace \
  && kubectl delete pvc -l app=cassandra
```
2. Run the following command to delete the Cassandra `Service`.

```shell
kubectl delete service -l app=cassandra
```

{% endcapture %}

{% capture whatsnext %}
* Learn how to [Scale a StatefullSet](https://kubernetes.io/docs/docs/tasks/run-application/scale-stateful-set/).
* Learn more about the [KubernetesSeedProvider](https://github.com/kubernetes/examples/blob/master/cassandra/java/src/main/java/io/k8s/cassandra/KubernetesSeedProvider.java)
* See more custom [Seed Provider Configurations](https://git.k8s.io/examples/cassandra/java/README.md)

{% endcapture %}

{% include templates/tutorial.md %}

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/cassandra/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
