## Reliable, Scalable Redis on Kubernetes

The following document describes the deployment of a reliable, multi-node Redis on Kubernetes.  It deploys a master with replicated slaves, as well as replicated redis sentinels which are use for health checking and failover.

### Prerequisites

This example assumes that you have a Kubernetes cluster installed and running, and that you have installed the ```kubectl``` command line tool somewhere in your path.  Please see the [getting started](../../../docs/getting-started-guides/) for installation instructions for your platform.

### Turning up an initial master/sentinel pod.

A [_Pod_](../../../docs/user-guide/pods.md) is one or more containers that _must_ be scheduled onto the same host.  All containers in a pod share a network namespace, and may optionally share mounted volumes.

We will use the shared network namespace to bootstrap our Redis cluster.  In particular, the very first sentinel needs to know how to find the master (subsequent sentinels just ask the first sentinel).  Because all containers in a Pod share a network namespace, the sentinel can simply look at ```$(hostname -i):6379```.

Here is the config for the initial master and sentinel pod: [redis-statefulset.yaml](redis-statefulset.yaml)


Create this master as follows:

```sh
kubectl create -f examples/storage/redis/redis-statefulset.yaml
```

### Scale our replicated pods

Initially creating those pods didn't actually do anything, since we only asked for one sentinel and one redis server, and they already existed, nothing changed.  Now we will add more replicas:

```sh
kubectl scale statefulset redis --replicas=3
```

This will create two additional replicas of the redis server and two additional replicas of the redis sentinel.

Unlike our original redis-master pod, these pods exist independently, and they use the ```redis-sentinel-service``` that we defined above to discover and join the cluster.

### Delete our manual pod

The final step in the cluster turn up is to delete the original redis-0 pod that we created manually.  While it was useful for bootstrapping discovery in the cluster, we really don't want the lifespan of our sentinel to be tied to the lifespan of one of our redis servers, and now that we have a successful, replicated redis sentinel service up and running, the binding is unnecessary.

Delete the master as follows:

```sh
kubectl delete pods redis-0
```

Check redis logs:
```sh
kubectl logs redis-0 -c redis
kubectl logs redis-0 -c sentinel

kubectl logs redis-1 -c redis
kubectl logs redis-1 -c sentinel

kubectl logs redis-2 -c redis
kubectl logs redis-2 -c sentinel
```

### Conclusion

At this point we now have a reliable, scalable Redis installation.  By scaling the replication controller for redis servers, we can increase or decrease the number of read-slaves in our cluster.  Likewise, if failures occur, the redis-sentinels will perform master election and select a new master.

**NOTE:** since redis 3.2 some security measures (bind to 127.0.0.1 and `--protected-mode`) are enabled by default. Please read about this in http://antirez.com/news/96


### tl; dr

For those of you who are impatient, here is the summary of commands we ran in this tutorial:

```
# Create a bootstrap master and sentinel
kubectl create -f examples/storage/redis/redis-statefulset.yaml

# Check logs of the master and sentinel
kubectl logs redis-0 -c redis
kubectl logs redis-0 -c sentinel

# Scale both replication controllers
kubectl scale statefulset redis --replicas=3

# Delete the original master pod
kubectl delete pods redis-0

# Check logs of all nodes
kubectl logs redis-0 -c redis
kubectl logs redis-0 -c sentinel

kubectl logs redis-1 -c redis
kubectl logs redis-1 -c sentinel

kubectl logs redis-2 -c redis
kubectl logs redis-2 -c sentinel
```


<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/storage/redis/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
