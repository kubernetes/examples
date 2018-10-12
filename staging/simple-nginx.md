## Running your first containers in Kubernetes

Ok, you've run one of the [getting started guides](https://kubernetes.io/docs/user-journeys/users/application-developer/foundational/#section-1) and you have
successfully turned up a Kubernetes cluster.  Now what?  This guide will help you get oriented
to Kubernetes and running your first containers on the cluster.

### Running a container (simple version)

From this point onwards, it is assumed that `kubectl` is on your path from one of the getting started guides.

The [`kubectl create`](https://kubernetes.io/docs/reference/generated/kubectl/kubectl-commands#create) line below will create a [deployment](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/) named `my-nginx` to ensure that there are always a [nginx](https://hub.docker.com/_/nginx/) [pod](https://kubernetes.io/docs/concepts/workloads/pods/pod-overview/) running.

```bash
kubectl create deployment --image nginx my-nginx
```

You can list the pods to see what is up and running:

```bash
kubectl get pods
```

You can also see the deployment that was created:

```bash
kubectl get deployment
```

You can also scale the deployment to ensure there is two nginx pods running:

```bash
kubectl scale deployment --replicas 2 my-nginx
```

You can now list the pods to see there is two up and running:

```bash
kubectl get pods
```

### Exposing your pods to the internet

On some platforms (for example Google Compute Engine) the kubectl command can integrate with your cloud provider to add a [public IP address](https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services---service-types) for the pods,
to do this run:

```bash
kubectl expose deployment my-nginx --port=80 --type=LoadBalancer
```

This should print the service that has been created, and map an external IP address to the service. Where to find this external IP address will depend on the environment you run in.  For instance, for Google Compute Engine the external IP address is listed as part of the newly created service and can be retrieved by running

```bash
kubectl get services
```

In order to access your nginx landing page, you also have to make sure that traffic from external IPs is allowed. Do this by opening a firewall to allow traffic on port 80.

### Cleanup

To delete the two replicated containers, delete the deployment:

```bash
kubectl delete deployment my-nginx
```

### Next: Configuration files

Most people will eventually want to use declarative configuration files for creating/modifying their applications.  A [simplified introduction](https://kubernetes.io/docs/user-journeys/users/application-developer/foundational/#section-2)
is given in a different document.


<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/simple-nginx.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
