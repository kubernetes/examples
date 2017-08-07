<!-- EXCLUDE_FROM_DOCS BEGIN -->

> :warning: :warning: Follow this tutorial on the Kubernetes website:
> https://kubernetes.io/docs/tutorials/stateless-application/guestbook/.
> Otherwise some of the URLs will not work properly.

## Creating a Guestbook
<!-- EXCLUDE_FROM_DOCS END -->

{% capture overview %}
This tutorial shows you how to build and deploy a simple, multi-tier web application using Kubernetes and [Docker](https://www.docker.com/). This example consists of the following components:

* A single-instance [Redis](https://redis.io/) master to store guestbook entries 
* Multiple replicated Redis instances to serve reads 
* Multiple web frontend instances

{% endcapture %}

{% capture objectives %}
* Start up a Redis master.
* Start up Redis slaves.
* Start up the guestbook frontend.
* Expose and view the Frontend Service.
* Clean up.
{% endcapture %}

{% capture prerequisites %}

{% include task-tutorial-prereqs.md %}
Download the following configuration files:

1. [redis-master-deployment.yaml](https://kubernetes.io/docs/tutorials/docs/tutorials/stateless-application/guestbook/redis-master-deployment.yaml)
1. [redis-master-service.yaml](https://kubernetes.io/docs/tutorials/docs/tutorials/stateless-application/guestbook/redis-master-service.yaml)
1. [redis-slave-deployment.yaml](https://kubernetes.io/docs/tutorials/docs/tutorials/stateless-application/guestbook/redis-slave-deployment.yaml)
1. [redis-slave-service.yaml](https://kubernetes.io/docs/tutorials/docs/tutorials/stateless-application/guestbook/redis-slave-service.yaml)
1. [frontend-deployment.yaml](https://kubernetes.io/docs/tutorials/docs/tutorials/stateless-application/guestbook/frontend-deployment.yaml)
1. [frontend-service.yaml](https://kubernetes.io/docs/tutorials/docs/tutorials/stateless-application/guestbook/frontend-service.yaml)

{% endcapture %}

{% capture lessoncontent %}

## Start up the Redis Master

The guestbook application uses Redis to store its data. It writes its data to a Redis master instance and reads data from multiple Redis slave instances.

### Creating the Redis Master Deployment

The manifest file, included below, specifies a Deployment controller that runs a single replica Redis master Pod.

1. Launch a terminal window in the directory you downloaded the manifest files.
2. Apply the Redis Master Deployment from the `redis-master-deployment.yaml` file:

       kubectl apply -f redis-master-deployment.yaml
        
   {% include code.html language="yaml" file="guestbook/redis-master-deployment.yaml" ghlink="/docs/tutorials/stateless-application/guestbook/redis-master-deployment.yaml" %}

3. Query the list of Pods to verify that the Redis Master Pod is running:

       kubectl get pods

   The response should be similar to this:

       NAME                            READY     STATUS    RESTARTS   AGE
       redis-master-1068406935-3lswp   1/1       Running   0          28s

4. Run the following command to view the logs from the Redis Master Pod:

       kubectl logs -f POD-NAME

**Note:** Replace POD-NAME with the name of your Pod.
{: .note}

### Creating the Redis Master Service

The guestbook applications needs to communicate to the Redis master to write its data. You need to apply a [Service](https://kubernetes.io/docs/concepts/services-networking/service/) to proxy the traffic to the Redis master Pod. A Service defines a policy to access the Pods.

1. Apply the Redis Master Service from the following `redis-master-service.yaml` file: 

       kubectl apply -f redis-master-service.yaml

   {% include code.html language="yaml" file="guestbook/redis-master-service.yaml" ghlink="/docs/tutorials/stateless-application/guestbook/redis-master-service.yaml" %}

**Note:** This manifest file creates a Service named `redis-master` with a set of labels that match the labels previously defined, so the Service routes network traffic to the Redis master Pod.   
{: .note}

2. Query the list of Services to verify that the Redis Master Service is running:

       kubectl get service

   The response should be similar to this:

       NAME           CLUSTER-IP   EXTERNAL-IP   PORT(S)    AGE
       kubernetes     10.0.0.1     <none>        443/TCP    1m
       redis-master   10.0.0.151   <none>        6379/TCP   8s

## Start up the Redis Slaves

Although the Redis master is a single pod, you can make it highly available to meet traffic demands by adding replica Redis slaves.

### Creating the Redis Slave Deployment

Deployments scale based off of the configurations set in the manifest file. In this case, the Deployment object specifies two replicas. 

If there are not any replicas running, this Deployment would start the two replicas on your container cluster. Conversely, if there are more than two replicas are running, it would scale down until two replicas are running. 

1. Apply the Redis Slave Deployment from the `redis-slave-deployment.yaml` file:

       kubectl apply -f redis-slave-deployment.yaml

   {% include code.html language="yaml" file="guestbook/redis-slave-deployment.yaml" ghlink="/docs/tutorials/stateless-application/guestbook/redis-slave-deployment.yaml" %}

2. Query the list of Pods to verify that the Redis Slave Pods are running:

       kubectl get pods

   The response should be similar to this:

       NAME                            READY     STATUS              RESTARTS   AGE
       redis-master-1068406935-3lswp   1/1       Running             0          1m
       redis-slave-2005841000-fpvqc    0/1       ContainerCreating   0          6s
       redis-slave-2005841000-phfv9    0/1       ContainerCreating   0          6s
        
### Creating the Redis Slave Service

The guestbook application needs to communicate to Redis slaves to read data. To make the Redis slaves discoverable, you need to set up a Service. A Service provides transparent load balancing to a set of Pods.

1. Apply the Redis Slave Service from the following `redis-slave-service.yaml` file:

       kubectl apply -f redis-slave-service.yaml

   {% include code.html language="yaml" file="guestbook/redis-slave-service.yaml" ghlink="/docs/tutorials/stateless-application/guestbook/redis-slave-service.yaml" %}

2. Query the list of Services to verify that the Redis Slave Service is running:

       kubectl get services

   The response should be similar to this:

       NAME           CLUSTER-IP   EXTERNAL-IP   PORT(S)    AGE
       kubernetes     10.0.0.1     <none>        443/TCP    2m
       redis-master   10.0.0.151   <none>        6379/TCP   1m
       redis-slave    10.0.0.223   <none>        6379/TCP   6s

## Set up and Expose the Guestbook Frontend

The guestbook application has a web frontend serving the HTTP requests written in PHP. It is configured to connect to the `redis-master` Service for write requests and the `redis-slave` service for Read requests.

### Creating the Guestbook Frontend Deployment

1. Apply the frontend Deployment from the following `frontend-deployment.yaml` file:

       kubectl apply -f frontend-deployment.yaml

   {% include code.html language="yaml" file="guestbook/frontend-deployment.yaml" ghlink="/docs/tutorials/stateless-application/guestbook/frontend-deployment.yaml" %}

2. Query the list of Pods to verify that the three frontend replicas are running:

       kubectl get pods -l app=guestbook -l tier=frontend

   The response should be similar to this:

       NAME                        READY     STATUS    RESTARTS   AGE
       frontend-3823415956-dsvc5   1/1       Running   0          54s
       frontend-3823415956-k22zn   1/1       Running   0          54s
       frontend-3823415956-w9gbt   1/1       Running   0          54s

### Creating the Frontend Service

The `redis-slave` and `redis-master` Services you applied are only accessible within the container cluster because the default type for a Service is [ClusterIP](https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services---service-types). `ClusterIP` provides a single IP address for the set of Pods the Service is pointing to. This IP address is accessible only within the cluster.

If you want guests to be able to access your guestbook, you must configure the frontend Service to be externally visible, so a client can request the Service from outside the container cluster. Minikube can only expose Services through `NodePort`.  

**Note:** Some cloud providers, like Google Compute Engine or Google Container Engine, support external load balancers. If your cloud provider supports load balancers and you want to use it, simply delete or comment out `type: NodePort`, and uncomment `type: LoadBalancer`. 
{: .note}

1. Apply the frontend Service from the following `frontend-service.yaml` file:

       kubectl apply -f frontend-service.yaml
        
   {% include code.html language="yaml" file="guestbook/frontend-service.yaml" ghlink="/docs/tutorials/stateless-application/guestbook/frontend-service.yaml" %}

2. Query the list of Services to verify that the frontend Service is running:

       kubectl get services 

   The response should be similar to this:

       NAME           CLUSTER-IP   EXTERNAL-IP   PORT(S)        AGE
       frontend       10.0.0.112   <nodes>       80:31323/TCP   6s
       kubernetes     10.0.0.1     <none>        443/TCP        4m
       redis-master   10.0.0.151   <none>        6379/TCP       2m
       redis-slave    10.0.0.223   <none>        6379/TCP       1m

### Viewing the Frontend Service via `NodePort`

If you deployed this application to Minikube or a local cluster, you need to find the IP address to view your Guestbook.

1. Run the following command to get the IP address for the frontend Service.

       minikube service frontend --url

   The response should be similar to this:

       http://192.168.99.100:31323

2. Copy the IP address, and load the page in your browser to view your guestbook.

### Viewing the Frontend Service via `LoadBalancer`

If you deployed the `frontend-service.yaml` manifest with type: `LoadBalancer` you need to find the IP address to view your Guestbook.

1. Run the following command to get the IP address for the frontend Service.

       kubectl get service frontend

   The response should be similar to this:

       NAME       CLUSTER-IP      EXTERNAL-IP        PORT(S)        AGE
       frontend   10.51.242.136   109.197.92.229     80:32372/TCP   1m

2. Copy the External IP address, and load the page in your browser to view your guestbook.

## Scale the Web Frontend 

Scaling up or down is easy because your servers are defined as a Service that uses a Deployment controller.

1. Run the following command to scale up the number of frontend Pods:

       kubectl scale deployment frontend --replicas=5

2. Query the list of Pods to verify the number of frontend Pods running:

       kubectl get pods

   The response should look similar to this: 

       NAME                            READY     STATUS    RESTARTS   AGE
       frontend-3823415956-70qj5       1/1       Running   0          5s
       frontend-3823415956-dsvc5       1/1       Running   0          54m
       frontend-3823415956-k22zn       1/1       Running   0          54m
       frontend-3823415956-w9gbt       1/1       Running   0          54m
       frontend-3823415956-x2pld       1/1       Running   0          5s
       redis-master-1068406935-3lswp   1/1       Running   0          56m
       redis-slave-2005841000-fpvqc    1/1       Running   0          55m
       redis-slave-2005841000-phfv9    1/1       Running   0          55m

3. Run the following command to scale down the number of frontend Pods:

       kubectl scale deployment frontend --replicas=2

4. Query the list of Pods to verify the number of frontend Pods running:

       kubectl get pods

   The response should look similar to this:

       NAME                            READY     STATUS    RESTARTS   AGE
       frontend-3823415956-k22zn       1/1       Running   0          1h
       frontend-3823415956-w9gbt       1/1       Running   0          1h
       redis-master-1068406935-3lswp   1/1       Running   0          1h
       redis-slave-2005841000-fpvqc    1/1       Running   0          1h
       redis-slave-2005841000-phfv9    1/1       Running   0          1h
        
{% endcapture %}

{% capture cleanup %}
Deleting the Deployments and Services also deletes any running Pods. Use labels to delete multiple resources with one command.

1. Run the following commands to delete all Pods, Deployments, and Services.

       kubectl delete deployment -l app=redis
       kubectl delete service -l app=redis
       kubectl delete deployment -l app=guestbook
       kubectl delete service -l app=guestbook

   The responses should be:

       deployment "redis-master" deleted
       deployment "redis-slave" deleted
       service "redis-master" deleted
       service "redis-slave" deleted
       deployment "frontend" deleted    
       service "frontend" deleted
       
2. Query the list of Pods to verify that no Pods are running:

       kubectl get pods
        
   The response should be this: 

       No resources found.
        
{% endcapture %}

{% capture whatsnext %}
* Complete the [Kubernetes Basics](https://kubernetes.io//docs/tutorials/kubernetes-basics/) Interactive Tutorials
* Use Kubernetes to create a blog using [Persistant Volumes for MySQL and Wordpress](https://kubernetes.io/docs/tutorials/stateful-application/mysql-wordpress-persistent-volume/#visit-your-new-wordpress-blog) 
* Read more about [connecting applications](https://kubernetes.io/docs/concepts/services-networking/connect-applications-service/)
* Read more about [Managing Resources](https://kubernetes.io/docs/concepts/cluster-administration/manage-deployment/#using-labels-effectively)
{% endcapture %}
{% include templates/tutorial.md %}

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/guestbook/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
