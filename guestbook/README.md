<!-- EXCLUDE_FROM_DOCS BEGIN -->

> :warning: :warning: Follow this tutorial on the Kubernetes website:
> https://kubernetes.io/docs/tutorials/stateless-application/guestbook/.
> Otherwise some of the URLs will not work properly.

## Creating a Guestbook
<!-- EXCLUDE_FROM_DOCS END -->

{% capture overview %}
This tutorial shows you how to build a simple, multi-tier web application using Kubernetes and [Docker](https://www.docker.com/). This example consists of the following:

* a web frontend 
* a [Redis](https://redis.io/) master for storage 
* a replicated set of Redis slaves. 

**Note:** If you are using a [Google Container Engine](https://cloud.google.com/container-engine/) installation, please read [Create a Guestbook with Redis and PHP](https://cloud.google.com/container-engine/docs/tutorials/guestbook) instead. The basic concepts are the same, but the walkthrough is tailored to a Container Engine setup.
{: .note}

{% endcapture %}

{% capture objectives %}
* Start up a redis master.
* Start up a redis slave.
* Start up the guestbook frontend.
* Expose and view the Frontend Service
* Clean up.
{% endcapture %}

{% capture prerequisites %}

{% include task-tutorial-prereqs.md %}
Download the following configuration files:
1. [redis-master-deployment.yaml](https://kubernetes.io/docs/tutorials//docs/tutorials/stateless-application/redis-master-deployment.yaml)
2. [redis-master-service.yaml](https://kubernetes.io/docs/tutorials//docs/tutorials/stateless-application/redis-master-service.yaml)
3. [redis-slave-deployment.yaml](https://kubernetes.io/docs/tutorials//docs/tutorials/stateless-application/redis-slave-deployment.yaml)
4. [redis-slave-service.yaml](https://kubernetes.io/docs/tutorials//docs/tutorials/stateless-application/redis-slave-service.yaml)
5. [frontend-deployment.yaml](https://kubernetes.io/docs/tutorials//docs/tutorials/stateless-application/frontend-deployment.yaml)
6. [frontend-service.yaml](https://kubernetes.io/docs/tutorials//docs/tutorials/stateless-application/frontend-service.yaml)

{% endcapture %}

{% capture lessoncontent %}

## Start up the Redis Master
The guestbook application uses Redis to store its data. It writes its data to a Redis master instance and reads data from multiple Redis worker (slave) instances.

### Creating the Redis Master Deployment
1. `cd` to the folder you saved the `.yaml.` files.
2. Create the Redis Master Deployment from the following `.yaml` file:

```shell
kubectl create -f redis-master-deployment.yaml
```
{% include code.html language="yaml" file="redis-master-deployment.yaml" ghlink="/docs/tutorials/docs/tutorials/stateless-application/redis-master-deployment.yaml" %}

{:start="3"}
3. Get the Pods to verify that the Redis Master Pod is running:

```shell
kubectl get pods
```

The response should be similar to this:

```shell
NAME                            READY     STATUS    RESTARTS   AGE
redis-master-1068406935-3lswp   1/1       Running   0          28s
```

{:start="4"}
4. Run the following command to view the logs from the Redis Master Pod:

**Note:** Replace POD-NAME with the name of your pod
{: .note}

```shell
kubectl logs -f POD-NAME
```

### Creating the Redis Master Service
The guestbook applications needs to communicate to the Redis master to write its data. You need to create a [Service](https://kubernetes.io/docs/concepts/services-networking/service/) to proxy the traffic to the Redis master Pod.

1. Create the Redis Master Service from the following `.yaml` file: 

```shell
kubectl create -f redis-master-service.yaml
```
{% include code.html language="yaml" file="redis-master-service.yaml" ghlink="/docs/tutorials/docs/tutorials/stateless-application/redis-master-service.yaml" %}

{:start="2"}
2. Get the Service to verify that the Redis Master Service is running:

```shell
kubectl get service
```

The response should be similar to this:

```shell
NAME           CLUSTER-IP   EXTERNAL-IP   PORT(S)    AGE
kubernetes     10.0.0.1     <none>        443/TCP    1m
redis-master   10.0.0.151   <none>        6379/TCP   8s
```

## Start up the Redis Slaves
Although the Redis master is a single pod, you can make it highly available to meet traffic demands by adding replica Redis workers.

### Creating the Redis Slave Deployment
Deployments scale based off of the configurations set in the manifest file. In this case, the Deployment object specifies two replicas. 

If there are not any replicas running, this Deployment would start the two replicas on your container cluster. Conversely, if there are more than two replicas are running, it would scale down until two replicas are running. 

1. Create the Redis Slave Deployment from the following `.yaml` file:

```shell
kubectl create -f redis-slave-deployment.yaml
```

{% include code.html language="yaml" file="redis-slave-deployment.yaml" ghlink="/docs/tutorials/docs/tutorials/stateless-application/redis-slave-deployment.yaml" %}

{:start="2"}
2. Get the Pods to verify that the Redis Slave Pods are running:

```shell
kubectl get pods
```

The response should be similar to this:
```shell
NAME                            READY     STATUS              RESTARTS   AGE
redis-master-1068406935-3lswp   1/1       Running             0          1m
redis-slave-2005841000-fpvqc    0/1       ContainerCreating   0          6s
redis-slave-2005841000-phfv9    0/1       ContainerCreating   0          6s
```
### Creating the Redis Slave Service
The guestbook application needs to communicate to Redis workers to read data. To make the Redis workers discoverable, you need to set up a Service. A Service provides transparent load balancing to a set of Pods.

1. Create the Redis Slave Service from the following `.yaml` file:

```shell
kubectl create -f redis-slave-service.yaml
```

{% include code.html language="yaml" file="redis-slave-service.yaml" ghlink="/docs/tutorials/docs/tutorials/stateless-application/redis-slave-service.yaml" %}

{:start="2"}
2. Get the Services to verify that the Redis Slave Service is running:

```shell
kubectl get services
```

The response should be similar to this:
```shell
NAME           CLUSTER-IP   EXTERNAL-IP   PORT(S)    AGE
kubernetes     10.0.0.1     <none>        443/TCP    2m
redis-master   10.0.0.151   <none>        6379/TCP   1m
redis-slave    10.0.0.223   <none>        6379/TCP   6s
```

## Set up and Expose the Guestbook Frontend
This tutorial uses a simple PHP server that is configured to talk to either the slave or master Services, depending on whether the client request is a read or a write. It exposes a simple JSON interface, and serves a jQuery-Ajax-based UX.

### Creating the Guestbook Frontend Deployment
1. Create the frontend Deployment from the following `.yaml` file:

```shell
kubectl create -f frontend-deployment.yaml
```

{% include code.html language="yaml" file="frontend-deployment.yaml" ghlink="/docs/tutorials/docs/tutorials/stateless-application/frontend-deployment.yaml" %}

{:start="2"}
2. Get the Pods to verify that the three frontend replicas are running:

```shell
kubectl get pods -l app=guestbook -l tier=frontend
```

The response should be similar to this:

```shell
NAME                        READY     STATUS    RESTARTS   AGE
frontend-3823415956-dsvc5   1/1       Running   0          54s
frontend-3823415956-k22zn   1/1       Running   0          54s
frontend-3823415956-w9gbt   1/1       Running   0          54s
```

### Creating the Frontend Service
The `redis-slave` and `redis-master` Services you created are only accessible within the container cluster because the default type for a Service is [ClusterIP](https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services---service-types). `ClusterIP` provides a single IP address for the set of Pods the Service is pointing to. This IP address is accessible only within the cluster.

If you want guests to be able to access your guestbook, you must configure the frontend Service to be externally visible, so a client can request the Service from outside the container cluster. Minikube can only expose Services through `NodePort`.  

**Note:** Some cloud providers, like Google Compute Engine or Google Container Engine, support external load balancers. If your cloud provider supports it, simply delete or comment out `type: NodePort`, and uncomment `type: LoadBalancer`. 
{: note}

1. Create the frontend Service from the following `.yaml` file:

```shell
kubectl create -f frontend-service.yaml
```

{% include code.html language="yaml" file="frontend-service.yaml" ghlink="/docs/tutorials/docs/tutorials/stateless-application/frontend-service.yaml" %}

{:start="2"}
2. Get the Services to verify that the frontend Service is running:

```shell
kubectl
get services 
```
The response should be similar to this:

```shell
NAME           CLUSTER-IP   EXTERNAL-IP   PORT(S)        AGE
frontend       10.0.0.112   <nodes>       80:31323/TCP   6s
kubernetes     10.0.0.1     <none>        443/TCP        4m
redis-master   10.0.0.151   <none>        6379/TCP       2m
redis-slave    10.0.0.223   <none>        6379/TCP       1m
```

### Viewing the Frontend Service via `NodePort`
Once the frontend Service is running, you need to find the IP address to view your Guestbook.

1. Run the following command to get the IP address for the frontend Service.

```shell
minikube service frontend --url
```

The response should be similar to this:

```shell
http://192.168.99.100:31323
```

{:start="2"}
2. Copy the IP address, and load the page in your browser to view your guestbook.

### Viewing the Frontend Service via `LoadBalancer`
Once the frontend Service is running, you need to find the IP address to view your Guestbook.

1. Run the following command to get the IP address for the frontend Service.


```shell
kubectl get service frontend
```

The response should be similar to this:

```shell
NAME       CLUSTER-IP      EXTERNAL-IP        PORT(S)        AGE
frontend   10.51.242.136   109.197.92.229     80:32372/TCP   1m
```

{:start="2"}
2. Copy the External IP address, and load the page.

## Scale the Web Frontend 
Scaling up or down is easy because your servers are defined as a Service that uses a Deployment controller.

1. Run the following command to scale up the number of frontend Pods:

```shell
kubectl scale deployment frontend --replicas=5
```

{:start="2"}
2. Get the Pods to verify the number of frontend Pods running:

```shell
kubectl get pods
```
    The response should look similar to this: 

```shel
NAME                            READY     STATUS    RESTARTS   AGE
frontend-3823415956-70qj5       1/1       Running   0          5s
frontend-3823415956-dsvc5       1/1       Running   0          54m
frontend-3823415956-k22zn       1/1       Running   0          54m
frontend-3823415956-w9gbt       1/1       Running   0          54m
frontend-3823415956-x2pld       1/1       Running   0          5s
redis-master-1068406935-3lswp   1/1       Running   0          56m
redis-slave-2005841000-fpvqc    1/1       Running   0          55m
redis-slave-2005841000-phfv9    1/1       Running   0          55m
```

{:start="3"}
3. Run the following command to scale down the number of frontend Pods:

```shell
kubectl scale deployment frontend --replicas=2
```

{:start="4"}
4. Get the Pods to verify the number of frontend Pods running:

```shell
kubectl get pods
```

    The response should look similar to this:

```shell
NAME                            READY     STATUS    RESTARTS   AGE
frontend-3823415956-k22zn       1/1       Running   0          1h
frontend-3823415956-w9gbt       1/1       Running   0          1h
redis-master-1068406935-3lswp   1/1       Running   0          1h
redis-slave-2005841000-fpvqc    1/1       Running   0          1h
redis-slave-2005841000-phfv9    1/1       Running   0          1h
```

{% endcapture %}

{% capture cleanup %}
Deleting the Deployments and Services also deletes any running Pods. Use labels to delete multiple resources with one command.

1. Run the following command to delete all Pods, Deployments, and Services.

```shell
kubectl delete deployments,services -l "app in (redis, guestbook)"
```
The response should be this:

```shell
deployment "frontend" deleted
deployment "redis-master" deleted
deployment "redis-slave" deleted
service "frontend" deleted
service "redis-master" deleted
service "redis-slave" deleted
```

{:start="2"}
2. Get the Pods to verify that no Pods are running:

```shell
kubectl get pods
```
The response should be this: 

```shell
No resources found.
```
{% endcapture %}

{% capture whatsnext %}
{% endcapture %}
* Read more about [connecting applications](https://kubernetes.io/docs/concepts/services-networking/connect-applications-service/)
* Read more about [Managing Resources](https://kubernetes.io/docs/concepts/cluster-administration/manage-deployment/#using-labels-effectively)

{% include templates/tutorial.md %}

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/guestbook/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
