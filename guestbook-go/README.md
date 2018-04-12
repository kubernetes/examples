## Guestbook Example

This example was forked from the official kubernetes [guestbook-go](https://github.com/kubernetes/examples/tree/master/guestbook-go) example.   It shows how to build a simple multi-tier web application using Kubernetes and Docker. The application consists of a web front end, Redis master for storage, and replicated set of Redis slaves, all for which we will create Kubernetes replication controllers, pods, and services.

As part of the journey to move the application to Istio service mesh, the guestbook application and service is moved to run in Istio.  Also, the guestbook application has been modernized so it can generate a cute emoji upon every single guest comment, by leveraging machine learning capability from IBM Watson Tone Anazlyer.

##### Table of Contents

 * [Step Zero: Prerequisites](#step-zero)
 * [Step One: Create the guestbook deployment and service](#step-one)
 * [Step Two: View the guestbook](#step-two)
 * [Step Three: Modernize the guestbook](#step-three)
 * [Step Four: View the modernize the guestbook](#step-four)
 * [Step Five: Cleanup](#step-five)

### Step Zero: Prerequisites <a id="step-zero"></a>

This example assumes that you have a working cluster. See the [Getting Started Guides](https://kubernetes.io/docs/setup/) for details about creating a cluster.

**Tip:** View all the `kubectl` commands, including their options and descriptions in the [kubectl CLI reference](https://kubernetes.io/docs/user-guide/kubectl-overview/).

Please follow the the [steps](https://github.com/kubernetes/examples/blob/master/guestbook-go/README.md) in the original example to create the redis master pod and service, create the redis slave pod and service.

Also, please follow the [istio.io](http://istio.io) official document to install Istio without mTLS on your k8s cluster.

### Step One: Create the guestbook deployment and service <a id="step-one"></a>

This is a simple Go `net/http` ([negroni](https://github.com/codegangsta/negroni) based) server that is configured to talk to either the slave or master services depending on whether the request is a read or a write. The pods we are creating expose a simple JSON interface and serves a jQuery-Ajax based UI. Like the Redis read slaves, these pods are also managed by a replication controller.

1. Use the [guestbook-deployment.yaml](guestbook-deployment.yaml) file to create the guestbook deployment by running the `kubectl create -f` *`filename`* command:

    ```console
    $ kubectl apply -f <(istioctl kube-inject -f guestbook-deployment.yaml --debug)
    ```

2. To verify that the guestbook deployment is running, run the `kubectl get deloyment` command.

3. To verify that the guestbook pods are running (it might take up to thirty seconds to create the pods), list the pods you created in cluster with the `kubectl get pods` command.

    Result: You see a single Redis master, two Redis slaves, and three guestbook pods.

4. Use the [guestbook-service.yaml](guestbook-service.yaml) file to create the guestbook service by running the `kubectl create -f` *`filename`* command:

    ```console
    $ kubectl create -f guestbook-service.yaml
    ```
5. To verify that the guestbook service is up, list the services you created in the cluster with the `kubectl get services` command:

    ```console
    $ kubectl get services
    NAME              CLUSTER_IP       EXTERNAL_IP       PORT(S)       SELECTOR               AGE
    guestbook         10.0.217.218     146.148.81.8      3000/TCP      app=guestbook          1h
    redis-master      10.0.136.3       <none>            6379/TCP      app=redis,role=master  1h
    redis-slave       10.0.21.92       <none>            6379/TCP      app-redis,role=slave   1h
    ...
    ```

    Result: The service is created with label `app=guestbook`.

### Step Two: View the guestbook <a id="step-two"></a>

You can now play with the guestbook that you just created by opening it in a browser (it might take a few moments for the guestbook to come up).

 * **Local Host:**
    If you are running Kubernetes locally, to view the guestbook, navigate to `http://localhost:3000` in your browser.

 * **Remote Host:**
    1. To view the guestbook on a remote host, locate the external IP of the load balancer in the **IP** column of the `kubectl get services` output. In our example, the internal IP address is `10.0.217.218` and the external IP address is `146.148.81.8` (*Note: you might need to scroll to see the IP column*).

    2. Append port `3000` to the IP address (for example `http://146.148.81.8:3000`), and then navigate to that address in your browser.

    Result: The guestbook displays in your browser:

    ![Guestbook](guestbook-page.png)


### Step Five: Cleanup <a id="step-eight"></a>

After you're done playing with the guestbook, you can cleanup by deleting the guestbook service and removing the associated resources that were created, including load balancers, forwarding rules, target pools, and Kubernetes replication controllers and services.

Delete all the resources by running the following `kubectl delete -f` *`filename`* command:

```console
$ kubectl delete -f examples/guestbook-go
guestbook-controller
guestbook
redid-master-controller
redis-master
redis-slave-controller
redis-slave
```

Tip: To turn down your Kubernetes cluster, follow the corresponding instructions in the version of the
[Getting Started Guides](https://kubernetes.io/docs/getting-started-guides/) that you previously used to create your cluster.


<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/guestbook-go/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
