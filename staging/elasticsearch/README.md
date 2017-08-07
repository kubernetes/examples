# Elasticsearch for Kubernetes

Kubernetes makes it trivial for anyone to easily build and scale [Elasticsearch](http://www.elasticsearch.org/) clusters. Here, you'll find how to do so.
Current Elasticsearch version is `5.5.1`.

[A more robust example that follows Elasticsearch best-practices of separating nodes concern is also available](production_cluster/README.md).

Current pod descriptors use an `emptyDir` for storing data in each data node container. This is meant to be for the sake of simplicity and [should be adapted according to your storage needs](../../docs/design/persistent-storage.md).

## Docker image

The [pre-built image](https://github.com/pires/docker-elasticsearch-kubernetes) used in this example will not be supported. Feel free to fork to fit your own needs, but keep in mind that you will need to change Kubernetes descriptors accordingly.

## Deploy

Let's kickstart our cluster with 1 instance of Elasticsearch.

```
kubectl create -f staging/elasticsearch/service-account.yaml
kubectl create -f staging/elasticsearch/es-svc.yaml
kubectl create -f staging/elasticsearch/es-rc.yaml
```

Let's see if it worked:

```
$ kubectl get pods
NAME             READY     STATUS    RESTARTS   AGE
es-kfymw         1/1       Running   0          7m
```

```
$ kubectl logs es-kfymw
[2017-08-07T16:55:12,912][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] initializing ...
[2017-08-07T16:55:13,023][INFO ][o.e.e.NodeEnvironment    ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] using [1] data paths, mounts [[/data (/dev/sda1)]], net usable_space [92.6gb], net total_space [94.3gb], spins? [possibly], types [ext4]
[2017-08-07T16:55:13,023][INFO ][o.e.e.NodeEnvironment    ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] heap size [247.5mb], compressed ordinary object pointers [true]
[2017-08-07T16:55:13,025][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] node name [aeeb186a-aee4-4215-8b51-b8019cbbd134], node ID [EoGZYDTZRhqlQ2_Cq2qGvA]
[2017-08-07T16:55:13,026][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] version[5.5.1], pid[9], build[19c13d0/2017-07-18T20:44:24.823Z], OS[Linux/4.4.52+/amd64], JVM[Oracle Corporation/OpenJDK 64-Bit Server VM/1.8.0_131/25.131-b11]
[2017-08-07T16:55:13,026][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] JVM arguments [-XX:+UseConcMarkSweepGC, -XX:CMSInitiatingOccupancyFraction=75, -XX:+UseCMSInitiatingOccupancyOnly, -XX:+DisableExplicitGC, -XX:+AlwaysPreTouch, -Xss1m, -Djava.awt.headless=true, -Dfile.encoding=UTF-8, -Djna.nosys=true, -Djdk.io.permissionsUseCanonicalPath=true, -Dio.netty.noUnsafe=true, -Dio.netty.noKeySetOptimization=true, -Dlog4j.shutdownHookEnabled=false, -Dlog4j2.disable.jmx=true, -Dlog4j.skipJansi=true, -XX:+HeapDumpOnOutOfMemoryError, -Xms256m, -Xmx256m, -Des.path.home=/elasticsearch]
[2017-08-07T16:55:14,393][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [aggs-matrix-stats]
[2017-08-07T16:55:14,393][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [ingest-common]
[2017-08-07T16:55:14,394][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [lang-expression]
[2017-08-07T16:55:14,394][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [lang-groovy]
[2017-08-07T16:55:14,394][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [lang-mustache]
[2017-08-07T16:55:14,394][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [lang-painless]
[2017-08-07T16:55:14,394][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [parent-join]
[2017-08-07T16:55:14,395][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [percolator]
[2017-08-07T16:55:14,395][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [reindex]
[2017-08-07T16:55:14,395][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [transport-netty3]
[2017-08-07T16:55:14,395][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [transport-netty4]
[2017-08-07T16:55:14,402][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] no plugins loaded
[2017-08-07T16:55:16,509][INFO ][o.e.d.DiscoveryModule    ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] using discovery type [zen]
[2017-08-07T16:55:17,533][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] initialized
[2017-08-07T16:55:17,536][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] starting ...
[2017-08-07T16:55:17,956][INFO ][o.e.t.TransportService   ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] publish_address {10.44.0.13:9300}, bound_addresses {10.44.0.13:9300}
[2017-08-07T16:55:17,971][INFO ][o.e.b.BootstrapChecks    ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] bound or publishing to a non-loopback or non-link-local address, enforcing bootstrap checks
[2017-08-07T16:55:21,083][INFO ][o.e.c.s.ClusterService   ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] new_master {aeeb186a-aee4-4215-8b51-b8019cbbd134}{EoGZYDTZRhqlQ2_Cq2qGvA}{y4aevupxRKyFekYHULuGjA}{10.44.0.13}{10.44.0.13:9300}, reason: zen-disco-elected-as-master ([0] nodes joined)
[2017-08-07T16:55:21,137][INFO ][o.e.g.GatewayService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] recovered [0] indices into cluster_state
[2017-08-07T16:55:21,143][INFO ][o.e.h.n.Netty4HttpServerTransport] [aeeb186a-aee4-4215-8b51-b8019cbbd134] publish_address {10.44.0.13:9200}, bound_addresses {10.44.0.13:9200}
[2017-08-07T16:55:21,143][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] started
```

So we have a 1-node Elasticsearch cluster ready to handle some work.

## Scale

Scaling is as easy as:

```
kubectl scale --replicas=3 rc es
```

Did it work?

```
$ kubectl get pods
NAME             READY     STATUS    RESTARTS   AGE
es-78e0s         1/1       Running   0          8m
es-kfymw         1/1       Running   0          17m
es-rjmer         1/1       Running   0          8m
```

Let's take a look at logs:

```
$ kubectl logs es-kfymw
[2017-08-07T16:55:12,912][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] initializing ...
[2017-08-07T16:55:13,023][INFO ][o.e.e.NodeEnvironment    ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] using [1] data paths, mounts [[/data (/dev/sda1)]], net usable_space [92.6gb], net total_space [94.3gb], spins? [possibly], types [ext4]
[2017-08-07T16:55:13,023][INFO ][o.e.e.NodeEnvironment    ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] heap size [247.5mb], compressed ordinary object pointers [true]
[2017-08-07T16:55:13,025][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] node name [aeeb186a-aee4-4215-8b51-b8019cbbd134], node ID [EoGZYDTZRhqlQ2_Cq2qGvA]
[2017-08-07T16:55:13,026][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] version[5.5.1], pid[9], build[19c13d0/2017-07-18T20:44:24.823Z], OS[Linux/4.4.52+/amd64], JVM[Oracle Corporation/OpenJDK 64-Bit Server VM/1.8.0_131/25.131-b11]
[2017-08-07T16:55:13,026][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] JVM arguments [-XX:+UseConcMarkSweepGC, -XX:CMSInitiatingOccupancyFraction=75, -XX:+UseCMSInitiatingOccupancyOnly, -XX:+DisableExplicitGC, -XX:+AlwaysPreTouch, -Xss1m, -Djava.awt.headless=true, -Dfile.encoding=UTF-8, -Djna.nosys=true, -Djdk.io.permissionsUseCanonicalPath=true, -Dio.netty.noUnsafe=true, -Dio.netty.noKeySetOptimization=true, -Dlog4j.shutdownHookEnabled=false, -Dlog4j2.disable.jmx=true, -Dlog4j.skipJansi=true, -XX:+HeapDumpOnOutOfMemoryError, -Xms256m, -Xmx256m, -Des.path.home=/elasticsearch]
[2017-08-07T16:55:14,393][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [aggs-matrix-stats]
[2017-08-07T16:55:14,393][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [ingest-common]
[2017-08-07T16:55:14,394][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [lang-expression]
[2017-08-07T16:55:14,394][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [lang-groovy]
[2017-08-07T16:55:14,394][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [lang-mustache]
[2017-08-07T16:55:14,394][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [lang-painless]
[2017-08-07T16:55:14,394][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [parent-join]
[2017-08-07T16:55:14,395][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [percolator]
[2017-08-07T16:55:14,395][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [reindex]
[2017-08-07T16:55:14,395][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [transport-netty3]
[2017-08-07T16:55:14,395][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] loaded module [transport-netty4]
[2017-08-07T16:55:14,402][INFO ][o.e.p.PluginsService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] no plugins loaded
[2017-08-07T16:55:16,509][INFO ][o.e.d.DiscoveryModule    ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] using discovery type [zen]
[2017-08-07T16:55:17,533][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] initialized
[2017-08-07T16:55:17,536][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] starting ...
[2017-08-07T16:55:17,956][INFO ][o.e.t.TransportService   ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] publish_address {10.44.0.13:9300}, bound_addresses {10.44.0.13:9300}
[2017-08-07T16:55:17,971][INFO ][o.e.b.BootstrapChecks    ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] bound or publishing to a non-loopback or non-link-local address, enforcing bootstrap checks
[2017-08-07T16:55:21,083][INFO ][o.e.c.s.ClusterService   ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] new_master {aeeb186a-aee4-4215-8b51-b8019cbbd134}{EoGZYDTZRhqlQ2_Cq2qGvA}{y4aevupxRKyFekYHULuGjA}{10.44.0.13}{10.44.0.13:9300}, reason: zen-disco-elected-as-master ([0] nodes joined)
[2017-08-07T16:55:21,137][INFO ][o.e.g.GatewayService     ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] recovered [0] indices into cluster_state
[2017-08-07T16:55:21,143][INFO ][o.e.h.n.Netty4HttpServerTransport] [aeeb186a-aee4-4215-8b51-b8019cbbd134] publish_address {10.44.0.13:9200}, bound_addresses {10.44.0.13:9200}
[2017-08-07T16:55:21,143][INFO ][o.e.n.Node               ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] started
[2017-08-07T16:58:52,532][INFO ][o.e.c.s.ClusterService   ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] added {{c8ad534d-4c6c-4700-898b-90e7a6520351}{GOSVvuRnTf27l859On7UQQ}{LvmvxUf4RWiup-GRsHIhGA}{10.44.0.14}{10.44.0.14:9300},}, reason: zen-disco-node-join[{c8ad534d-4c6c-4700-898b-90e7a6520351}{GOSVvuRnTf27l859On7UQQ}{LvmvxUf4RWiup-GRsHIhGA}{10.44.0.14}{10.44.0.14:9300}]
[2017-08-07T16:58:52,628][WARN ][o.e.d.z.ElectMasterService] [aeeb186a-aee4-4215-8b51-b8019cbbd134] value for setting "discovery.zen.minimum_master_nodes" is too low. This can result in data loss! Please set it to at least a quorum of master-eligible nodes (current value: [1], total number of master-eligible nodes used for publishing in this round: [2])
[2017-08-07T16:58:56,246][INFO ][o.e.c.s.ClusterService   ] [aeeb186a-aee4-4215-8b51-b8019cbbd134] added {{d6f412cd-3ef0-45c2-9f56-c9f62fd35e9c}{ne558ZbWTkK9v_mnzSDcYA}{_XOZrkQQT8Solatn1WRO4Q}{10.44.0.15}{10.44.0.15:9300},}, reason: zen-disco-node-join[{d6f412cd-3ef0-45c2-9f56-c9f62fd35e9c}{ne558ZbWTkK9v_mnzSDcYA}{_XOZrkQQT8Solatn1WRO4Q}{10.44.0.15}{10.44.0.15:9300}]
```

So we have a 3-node Elasticsearch cluster ready to handle more work.

## Access the service

*Don't forget* that services in Kubernetes are only acessible from containers in the cluster. For different behavior you should [configure the creation of an external load-balancer](http://kubernetes.io/v1.0/docs/user-guide/services.html#type-loadbalancer). While it's supported within this example service descriptor, its usage is out of scope of this document, for now.

```
$ kubectl get service elasticsearch
NAME            CLUSTER-IP     EXTERNAL-IP      PORT(S)                         AGE
elasticsearch   10.47.253.42   35.189.128.215   9200:31959/TCP,9300:32749/TCP   18m
```

From any host on your cluster (that's running `kube-proxy`), run:

```
$ curl 35.189.128.215:9200
```

You should see something similar to the following:


```json
{
  "name" : "c8ad534d-4c6c-4700-898b-90e7a6520351",
  "cluster_name" : "myesdb",
  "cluster_uuid" : "KhEJKtkgTq-BeOpgJsGMaQ",
  "version" : {
    "number" : "5.5.1",
    "build_hash" : "19c13d0",
    "build_date" : "2017-07-18T20:44:24.823Z",
    "build_snapshot" : false,
    "lucene_version" : "6.6.0"
  },
  "tagline" : "You Know, for Search"
}
```

Or if you want to check cluster information:


```
curl 35.189.128.215:9200/_cluster/health?pretty
```

You should see something similar to the following:

```json
{
  "cluster_name" : "myesdb",
  "status" : "green",
  "timed_out" : false,
  "number_of_nodes" : 3,
  "number_of_data_nodes" : 3,
  "active_primary_shards" : 0,
  "active_shards" : 0,
  "relocating_shards" : 0,
  "initializing_shards" : 0,
  "unassigned_shards" : 0,
  "delayed_unassigned_shards" : 0,
  "number_of_pending_tasks" : 0,
  "number_of_in_flight_fetch" : 0,
  "task_max_waiting_in_queue_millis" : 0,
  "active_shards_percent_as_number" : 100.0
}
```

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/elasticsearch/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
