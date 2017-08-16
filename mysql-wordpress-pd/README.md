<!-- EXCLUDE_FROM_DOCS BEGIN -->

> :warning: :warning: Follow this tutorial on the Kubernetes website:
> https://kubernetes.io/docs/tutorials/stateful-application/mysql-wordpress-persistent-volume/.
> Otherwise some of the URLs will not work properly.

# Using Persistent Volumes with MySQL and WordPress
<!-- EXCLUDE_FROM_DOCS END -->

<!-- EXCLUDE_FROM_DOCS BEGIN -->

> :warning: :warning: Follow this tutorial on the Kubernetes website:
> https://kubernetes.io/docs/tutorials/stateful-application/mysql-wordpress-persistent-volume/.
> Otherwise some of the URLs will not work properly.

# Using Persistent Volumes with MySQL and WordPress
<!-- EXCLUDE_FROM_DOCS END -->

{% capture overview %}
This tutorial shows you how to deploy a WordPress site and a MySQL database using Minikube. Both applications use PersistentVolumes and PersistentVolumeClaims to store data. 

A [PersistentVolume](https://kubernetes.io/docs/concepts/storage/persistent-volumes/) (PV) is a set amount of storage in a cluster, and a [PeristantVolume Claim](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims) (PVC) is an set ammout of storage in a PV. PVs and PVCs are independent from Pod lifecycles and preserve data through restarting, rescheduling, and even deleting Pods. 

**Wanring:** This tutorial uses Minikube to run a local, single-node Kubernetes cluster. Do not make this your permanent WordPress Deployment.
{: .warning}

{% endcapture %}

{% capture objectives %}
* Create a PersistentVolume
* Create a Secret
* Deploy MySQL
* Deploy WordPress
* Clean up

{% endcapture %}

{% capture prerequisites %}

{% include task-tutorial-prereqs.md %} 

Download the following configuration files:

1. [local-volumes.yaml](https://kubernetes.io/docs/tutorials/stateful-application/local-volumes.yaml)

2. [mysql-deployment.yaml](https://kubernetes.io//docs/tutorials/stateful-application/mysql-deployment.yaml)

3. [wordpress-deployment.yaml](https://kubernetes.io/docs/tutorials/stateful-application/wordpress-deployment.yaml)

{% endcapture %}

{% capture lessoncontent %} 

## Create a PersistentVolume

MySQL and Wordpress each use a Persistent Volume to store data. While Kubernetes supports many different [Types of PersistentVolumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#types-of-persistent-volumes), this tutorial covers [hostPath](https://kubernetes.io/docs/concepts/storage/volumes/#hostpath).

**Note:** If you have a Kubernetes cluster running on Google Container Engine, please follow [this guide](https://cloud.google.com/container-engine/docs/tutorials/persistent-disk).
{: .note}

### Setting up a hostPath Volume

A `hostPath` volume emulates network-attached storage. 

**Warning:** Only use `hostPaths` for developing and testing. Data does not move between nodes. If a Pod dies, the data disappears even if the Pod restarts on a new node. 
{: .warning}

1. Launch a terminal window in the directory you downloaded the manifest files.

2. Create a Persistent Volume from the `local-volumes.yaml` file:

       kubectl create -f local-volumes.yaml

{% include code.html language="yaml" file="local-volumes.yaml" ghlink="/docs/tutorials/stateful-application/local-volumes.yaml %}

{:start="3"} 
3. Run the following command to verify that two 20GiB PVs are available:

       kubectl get pv

   The response should be like this:

       NAME         CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS      CLAIM     STORAGECLASS   REASON    AGE
       local-pv-1   20Gi       RWO           Retain          Available                                      1m
       local-pv-2   20Gi       RWO           Retain          Available                                      1m

## Create a Secret

A [Secret](https://kubernetes.io/docs/concepts/configuration/secret/) is an object that stores a piece of sensitive data like a password or key. The manifest files are already configured to use a Secret, but you have to create your own Secret.

1. Create a file called `password.txt` in the same directory as the manifest files.

2. Set a password by typing your desired password, and then save the file. 

   **Caution:** Make sure that there is no trailing newline at the end of the password.
   {: .caution}

3. Create the Secret object from the following command:

       kubectl create secret generic mysql-pass --from-file=password.txt

4. Verify that the Secret exists by running the following command:

       kubectl get secrets

   The response should be like this:

       NAME                  TYPE                                  DATA      AGE
       mysql-pass            Opaque                                1         42s

   **Note:** To protect the Secret from exposure, neither `get` nor `describe` show its contents. 
   {: .note}

## Deploy MySQL

The following manifest describes a single-instance MySQL Pod Deployment. The MySQL container mounts the PersistentVolume at /var/lib/mysql. The `MYSQL_ROOT_PASSWORD` environment variable sets the database password from the Secret. 

{% include code.html language="yaml" file="mysql-deployment.yaml" ghlink="/docs/tutorials/stateful-application/mysql-deployment.yaml %}

1. Deploy MySQL from the `mysql-deployment.yaml` file:

       kubectl create -f mysql-deployment.yaml

   **Note:** It can take up to a few minutes for the Pod's Status to be `RUNNING`.
   {: .note}

2. Verify that the Pod is running by running the following command:

       kubectl get pods

   The response should be like this:

       NAME                               READY     STATUS    RESTARTS   AGE
       wordpress-mysql-1894417608-x5dzt   1/1       Running   0          40s


## Deploy WordPress

The following manifest describes a single-instance WordPress Pod Deployment. It uses many of the same features like a PVC for persistent storage and a Secret for the password. But it also uses a different setting: `"type": NodePort`. This setting exposes WordPress to traffic from outside of the cluster.

{% include code.html language="yaml" file="mysql-deployment.yaml" ghlink="/docs/tutorials/stateful-application/wordpress-deployment.yaml %}

1. Deploy WordPress from the `wordpress-deployment.yaml` file:

       kubectl create -f wordpress-deployment.yaml

2. Verify that the Service is running by running the following command:

       kubectl get services wordpress

   The response should be like this:

       NAME        CLUSTER-IP   EXTERNAL-IP   PORT(S)        AGE
       wordpress   10.0.0.89    <pending>     80:32406/TCP   4m

   **Note:** Minikube can only expose Services through `NodePort`. <br/><br/>The `EXTERNAL-IP` is always `<pending>`.
   {: .note}

3. Run the following command to get the IP Address for the WordPress Service:

       minikube service wordpress --url

   The response should be like this:

       http://1.2.3.4:32406

4. Copy the IP address, and load the page in your browser to view your blog.

   You should see the familiar WordPress init page.

   ![wordpress-init](https://github.com/kubernetes/examples/blob/master/mysql-wordpress-pd/WordPress.png)

   **Warning:** Do not leave your WordPress installation on this page. If another user finds it, they can set up a website on your instance and use it to serve malicious content. <br/><br/>Either install WordPress by creating a username and password or delete your instance.
   {: .warning}

{% endcapture %}

{% capture cleanup %}

1. Run the following command to delete your Secret:

       kubectl delete deployment -l app=wordpress

2. Run the following commands to delete all Deployments and Services:

       kubectl delete deployment -l app=wordpress
       kubectl delete service -l app=wordpress

   **Note:** Any other Type of PersistentVolume would allow you to recreate the Deployments and Services at this point without losing data, but `hostPath` loses the data as soon as the Pod stops running.
   {: .note}   

3. Run the following commands to delete the PersistentVolumeClaims and the PersistentVolumes:

       kubectl delete pvc -l app=wordpress
       kubectl delete pv local-pv-1 local-pv-2

{% endcapture %}

{% capture whatsnext %}

* [Introspection and Debugging](https://kubernetes.io/docs/tasks/debug-application-cluster/debug-application-introspection/)
* [Jobs](https://kubernetes.io/docs/concepts/workloads/controllers/jobs-run-to-completion/)
* [Exec](https://kubernetes.io/docs/tasks/debug-application-cluster/get-shell-running-container/)
* [Port Forwarding](https://kubernetes.io/docs/tasks/access-application-cluster/port-forward-access-application-cluster/)

{% endcapture %}

{% include templates/tutorial.md %}

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/mysql-wordpress-pd/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
