# Example: K8S-based OVS SDN

This example shows how to build a simple Software-Defined-Networking
(SDN) control plane using Kubernetes API machinery to build the
control plane and Kubernetes itself to host the control plane.  The
purpose is to explore how well suited the Kubernetes API-machinery is
to building something other than Kubernetes.  Some interesting issues
are surfaced in the discussions of the controllers here.

The data plane is based on OVS.  Each node has an independent OVS
installation.  The kube-based control plane distributes the needed
information to each node.

This SDN produces Prometheus metrics.  This example includes example
manifests for deploying Prometheus and Grafana in a way that will
scrape and display metrics from the cluster, including the SDN.


## Status

This is an early work in progress.  Currently there are initial drafts
of the kube API objects, the API extension server that holds them, the
controller that validates API objects of kind `subnet`, the interface
to the local network machinery, the controller that assigns IP addresses,
the controller that invokes the local network machinery, an implementation
of that interface that just logs invocations, and a test driver.


## Architecture

This example augments a Kubernetes cluster with virtual networks as a
kind of workload.  These virtual networks are _not_ used for the
kubernetes networking.  This example can be added on to any Kubernetes
cluster, with any networking solution, as long as the nodes have the
`openvswitch-switch` package installed and can communicate with each
other using their `InternalIP` addresses.

This example adds the following to the Kubernetes cluster.

- The [etcd Operator](https://github.com/coreos/etcd-operator),
  release 0.9.3

- An etcd cluster managed by the etcd Operator.  The cluster initially
  has three members and that number can be adjusted in the usual way
  for the Operator.  The etcd cluster is secured with TLS certs and
  keys that [the Makefile](Makefile) keeps on the filesystem of the
  build/ops machine.

- An API extension server Deployment.  The deployment initially has
  two members and the number can be adjusted in the usual ways.  These
  extension servers serve the custom resources used in this example,
  using the etcd cluster for storage.

- A Controller Manager Deployment. It is meant to be a singleton. It
  runs some of the controllers that KOS needs. Logically, each controller
  is a separate process, but to reduce complexity, they are all compiled
  into a single binary and run in a single process, in the same way the
  [Kubernetes controller manager](https://kubernetes.io/docs/concepts/overview/components/#kube-controller-manager)
  does. These controllers are:
  - [Subnet validation controller](#the-subnet-validation-controller):
    Responsible for validation of API objects of kind `subnet` and marking
    them as validated or not.
  - [IPAM controller](#the-ipam-controller): Responsible for assigning IP
    addresses to NetworkAttachments.

- A DaemonSet of connection agents that implement the node-local
  functionality of the SDN, by talking to the kube API machinery and
  the local networking implementation.

- A local networking implementation that has no control plane
  relationship with its counterpart on any other node.  This is a code
  library that has a golang interface and is compiled into the connection
  agent.

All components are created in the Kubernetes API namespace
`example-com`.

The controllers (i.e., subnets validator, IPAM controller, connection
agents) connect directly to the API extension servers rather than
utilizing the proxying ability of the Kubernetes main apiservers,
avoiding unnecessary load and configuration challenges on those main
apiservers.  The connections are made to the DNS name of the Kubernetes
service implemented by the API extension servers, and this engages the
TCP connection load balancing of Kubernetes (i.e., normally the kube-proxy).
If the number of worker nodes is low then this load balancing may not be
very balanced, and adjustments are made only as new TCP connections are made.


## The SDN

This example creates a simple SDN.  It is deliberately simple, so that
this example can be relatively easy to read and understand.  The SDN
has just enough complexity to show some interesting issues.  The
implementation is intended to be efficient and reliable at scale.

This SDN is not connected to anything.  It is not used by a CNI
plugin.  It is not used by any virtual machines.  It is a simple
stand-alone SDN.  It creates Linux network interfaces, but does not
connect them to anything.  It does not even implement DHCP nor
`dhclient` those network interfaces nor bind their IP addresses in any
other way.

The SDN implements virtual networks, using VXLAN encapsulation.  This
SDN does not attempt to support broadcast nor multicast traffic.  This
SDN avoids the problem of unicast to an unknown address by eagerly
disseminating the needed guestIP -> hostIP mappings to the possible
senders.

An SDN with as simple an API as this one could conceivably use a lazy
dissemination scheme for the guestIP -> hostIP mappings.  However,
that would conflict with the goals of this SDN in one or two ways.  A
lazy dissemination scheme would be more complex, and might not have
one of the key problems that this example is intended to illustrate
--- while a more realistic SDN _would_ have this eager dissemination
problem (e.g., for the ipset of a security group).

The SDN has just four concepts, as follows.

- VNI, a VXLAN virtual network identifier.  Usually represented by a
  golang `uint32`.  VNIs are chosen by the clients that create
  Subnets. A VXLAN virtual network is, by design choice, confined to
  a single K8s API namespace, but multiple virtual networks can reside
  in the same namespace.

- Subnet, a kube custom resource.  A subnet has a single CIDR block
  compliant with [RFC 1918](https://tools.ietf.org/html/rfc1918),
  from which addresses are assigned (with the usual omissions).  Each
  Subnet is on a VNI, and a VNI can have multiple Subnets. Both the
  CIDR block and the VNI are immutable: attempts to update them will
  fail.

- NetworkAttachment, a kube custom resource.  A NetworkAttachment is a
  request and a response to that request for a Linux network interface
  attached to a particular Subnet on a particular node. Both the subnet
  and the node are immutable: attempts to update them will fail.
  NetworkAttachment has an IP address and a MAC address that are chosen
  by the SDN.

- IPLock, a kube custom resource.  An IPLock is the hard state that
  stipulates the usage of a particular IP address for a particular
  NetworkAttachment.

A controller chooses the IP address for each NetworkAttachment.  An
attachment's MAC address is a function of the IP address and the VNI.

Neither controller enforces any interlock between its actions and the
lifecycle of any API object. Consequently, there can not be any fully
effective enforcement of immutability of any field of any API object ---
the client can always delete an object and create a replacement with the
same name, possibly without the controller seeing any intermediate state.
Coping with all possible changes makes for complicated controller code.
Stay tuned for an alternative version of `kos` which will feature
lifecycle interlocks to investigate their usefulness in simplifying
controllers.

Neither controller enforces or assumes any connections between the
lifecycles of NetworkAttachments and their Subnets; either can be
created or deleted at any time.  There are, however, some intended
consistency constraints between certain fields of certain objects.
The apiservers attempt to enforce this consistency, but this
enforcement is necessarily imperfect because there are no ACID
transactions that involve more than one object.  The controllers
therefore must be prepared to react safely if and when an
inconsistency gets through.  Again, this makes for very complicated
controller code.

The problems that the SDN solves are as follows.

- Subnets must be consistent. More precisely, for a given VNI all the
  subnets with that VNI must have disjoint CIDRs and must be within the
  same Kubernetes API namespace.

- IP addresses must be assigned to the NetworkAttachments.  For a
  given VNI, a given IP address can be assigned to at most one
  NetworkAttachment at a time.

- For a given VNI, the guestIP->hostIP mapping for each of the VNI's
  NetworkAttachments must be communicated to every node that has a
  NetworkAttachment with that VNI --- and should not be communicated to
  other nodes (because that communication would impose unnecessary
  costs).


### The SDN Datapath

On each worker node this SDN creates one OvS "bridge", named `kos`.
In addition to the implied port named after the bridge, the SDN
creates one special OVS port.  It is named `vtep` and, as its name
suggests, it exchanges encapsulated traffic with its peers on other
nodes (relying on the IP transport abilities of the nodes).

For each local NetworkAttachment, the SDN creates another OVS port on
the `kos` bridge, and a Linux network interface connected to that port.
The name and MAC address of such interface are reported in the
NetworkAttachment's status. The SDN does not impose IP layer configuration
on this Linux network interface (this might not work well, due to the
potentially overlapping IPs between virtual networks) but rather leaves
that up to the client.

To enable communication between Network interfaces, OpenFlow is used.
Two OpenFlow tables in each OvS bridge are populated by the SDN at
runtime: one for identifying the tunnel (VNI) of the packets and one
for actually forwarding data link traffic and ARP requests to the
appropriate interfaces. When processing packets, the two tables are
consulted in pipeline, with the tunneling one being examined first.

At bridge `kos` creation time, the SDN installs one "default"
OpenFlow flow in each table, which handles packets matching no
other flow. The default flow in the tunneling table forwards
packets to the other table. The default flow in the datalink traffic
and ARP table drops packets.

For each local NetworkAttachment, the SDN creates three OpenFlow flows
in the bridge, one in the tunneling table and two in the datalink and
ARP table. The flow in the tunneling table sets the tunnel ID of packets
sent from the NetworkAttachment's interface to the NetworkAttachment's
VNI, and forwards such packets to the other table. The two other flows
forward datalink traffic and ARP resolution requests destined to the
NetworkAttachment to its interface.

For each remote NetworkAttachment, the SDN creates two OpenFlow flows
in the bridge in the datalink and ARP table. These flows match datalink
traffic and ARP resolution requests sent to the remote NetworkAttachment.
Upon matching a packet, they set the tunnel endpoint to the node of the
remote NetworkAttachment and forward the packet to the `vtep` port. From
there, OvS will take care of forwarding the packet to the bridge in the
remote NetworkAttachment node. The flows installed in that bridge will
make it possible to deliver the packet to the NetworkAttachment's interface.


## The Subnet Validation Controller

This is a singleton that runs within the Controller Manager and attempts
to solve the problem of enforcing the two following invariants:

  1. There are no subnets with the same VNI and overlapping CIDR blocks.
  2. There are no subnets with the same VNI and different Kubernetes API
     namespaces.

The first is intrinsic in the nature of virtual networks and subnets,
while the second is a consequence of the design choice of confining VXLAN
virtual networks to a single Kubernetes API namespace.

The general problem here is enforcement of multi-objects invariants. For
such a simple SDN as `kos`, we could have ignored it, leaving it to the
user or to a higher-level UI. Since we believe this is a relevant problem
in distributed systems and we're building `kos` to assess Kubernetes API
machinery suitability for building distributed systems other than
Kubernetes itself, we chose to tackle it.

A request to create a subnet *X* with VNI *V* which violates any of the
invariants should fail. The only way to get this behavior is having the
extension API server do the validation, because it is the only component
to see *X* **before** it has already been created. But this alone is not
reliable. When *X* is being validated, all the other existing subnets with
VNI *V* must be retrieved, and each one of them must be compared to *X*
to check if an invariant is violated. But different API objects are not
necessarily validated sequentially by the extension API server. If a
subnet *Y* in *conflict* with *X* (i.e, *X* and *Y* mutual existences
lead to a violation of one invariant) is validated in parallel with *X*,
they will both be created. This case must be handled, but it is a rare
occurrence, so we chose to keep the validation by the API server
nevertheless.

To solve the problem also in the cases where two conflicting subnets
are created, we replaced the hard-to-enforce constraint that two subnets
violating the invariants cannot exist with the easier-to-enforce constraint
that if two subnets violating the invariants exist, subnet consumers use
at most one. To achieve this, each subnet has a `bool` field called
`SubnetStatus.Validated`. Upon creation it is set to false by the extension
API server. Consumers of subnets (currently only the
[IPAM controller](#the-ipam-controller)) can use a subnet only when
`SubnetStatus.Validated` is `true`. Once a subnet's `SubnetStatus.Validated`
becomes `true` it is guaranteed to stay `true` for the whole lifetime of
the subnet, although deleting the subnet and then quickly creating one with
the same name can practically look like a subnet update where
`SubnetStatus.Validated` transitions from `true` to `false` to a consumer
which did not see any intermediate state.

The subnet validator's task is setting `SubnetStatus.Validated` as
appropriate for every subnet. It uses an Informer to remain appraised of
all the subnets and follows the usual Kubernetes controller design pattern
based on a rate limiting queue and worker goroutines. When a worker processes
an existing subnet *X*, it does a live list of all the existing subnets
against the extension API server, and compares *X* against the results of the
list. If no violation of the invariants is found, *X*'s
`SubnetStatus.Validated` is set to `true`. The validator also keeps a
*conflicts cache* which associates each processed subnet *Y*'s namespaced
name to *Y*'s UID and a list with the namespaced names of the subnets whose
validation failed because of a conflict with *Y*. If, while validating *X*,
a conflict with *Y* is found, *Y*'s *UID* is compared to that stored in *Y*'s
namespaced name *conflicts cache* entry. If they match, *X*'s status is
updated by adding information on the conflict, and *X*'s namespaced name is
added to the conflicts cache entry for *Y*'s namespaced name. If they do not
match, no further action is taken and *X* is processed again after a delay.
If *Y* is deleted the *conflicts cache* entry for its namespaced name is
deleted and all the namespaced names in it are enqueued so that their
subnets can be re-validated: they did not pass validation because of a
conflict with *Y*, but *Y* no longer exists so the conflict might have
disappeared and they might be successfully validated now. The same happens
if a subnet *Y1* with the same namespaced name as *Y* is created shortly
after *Y*'s deletion and the validator misses the deletion and processes
*Y1* directly, the only difference being that the *conflicts cache* entry
for *Y* and *Y1*'s namespaced name is kept and reset by overriding *Y*'s UID
with *Y1*'s and resetting the list of conflicting subnets' namespaced names.
Making the addition of *X* to the conflicts cache corresponding to *Y*'s
namespaced name fail on mistach with the *UID* in that conflict cache
correctly handles the possible concurrent execution where one goroutine
processing *X* finds a conflict with *Y*, then *Y* is deleted and immediately
after another subnet *Y1* with the same namespaced name but different VNI
and CIDR block is created and a different goroutine processes *Y1*, then the
first goroutine gets around to trying to add *X* to the conflict cache which
belonged to *Y* and now belongs to *Y1*; doing that addition in this situation
would falsely indicate that *X* does not need to be reconsidered until *Y1*
is deleted.

One apparently odd design choice is having the validator retrieve all the
subnets with a live list against the API server rather than a cache-based
list using its Informer. The live list is worse performance-wise, but is
necessary to avoid race conditions that can lead to two conflicting subnets
having both `SubnetStatus.Validated` set to `true`. The subnets validator is
intended to be a singleton, but Kubernetes makes no guarantee that there
cannot be transients with more than one running. Assume two --- *V1* and *V2*
--- are running and cache-based lists are used. Also assume a subnet *X* is
created and shortly after another subnet *Y* in conflict with *X* is created.
*X* and *Y* might appear in *V1* and *V2*'s Informers' caches in opposite
orders: *V1* processes *X* first, and when it does the cache-based list *Y*
is not in the cache yet. Likewise, *V2* might validate *Y* without seeing *X*.
Hence both *X* and *Y* are marked as validated even if they are in conflict. The
problem is due to the fact that Informer's caches are not populated in order.
API objects are stored in `etcd` in a sequential order represented by a
*resource version* (basically a logical clock). Informers caches are populated
through two API calls: *LIST* and *WATCH*. A *LIST* is a request-response
which returns all the API objects matching certain parameters, while a
*WATCH* is a long-lived request that returns a stream of create/udpate/delete
notifications for API objects starting at a given *resource version*
(typically representing the state of `etcd` corresponding to a previous
*LIST* result). Notifications delivered through a *WATCH* are applied to
Informers caches in resource version order, while the results of a *LIST* are
not. Thus, if while validating *X* and *Y* *V2*'s Informer cache is being
populated through a *LIST*, *Y* might appear in its Informer cache before *X*
even if it's the newest. On the other hand, results for live lists against the
API server are populated from `etcd`: if *Y* already exists, there's the
guarantee that *X* is also in the results. Hence, using live lists instead of
cache-based ones prevents the race condition described above.


## The IPAM Controller

This is a singleton that runs within the Controller Manager and assigns IP
addresses to NetworkAttachments.

The following three approaches were considered, with the last one
taken.

One approach would be to make the IP address controller keep itself
appraised of all IP address assignments and assign unused addresses
when needed based on an in-memory cache of all the assigned addresses.
This can be correct only if it is impossible for there to be
two such processes running at once.  Kubernetes does not actually make
such a guarantee.  Although Kubernetes includes mechanisms to detect
failures and recover from them, no failure detector is perfect.

Another approach would be to keep a bitmap in API objects indicating
which addresses have been taken.  Not one bitmap --- that would be too
big --- but some suitable structure that holds one bit per "possible"
address.  The problem with this approach is that the Kubernetes API
machinery does not support any ACID transaction that involves more
than one object.  It is impossible to simultaneously update that
allocation table _and_ a NetworkAttachment.  The address controller
would have to update one first and then the other.  If the controller
crashes in between those two operations then an address will either be
used without being marked as taken or be lost to all future use.

The approach taken is to use the Kubernetes API machinery to construct
locks on IP addresses.  A lock is implemented by an API object whose
name is a function of the VNI and the IP address.  Attempting to take
a lock amounts to trying to create the IP lock object; creation of the
object equals successfully taking the lock.  A lock object's
`ObjectMeta.OwnerReferences` include a reference to the
NetworkAttachment that holds the lock.

As with any controller, one of the IP address controller's problems is
how to avoid doing duplicate work while waiting for its earlier
actions to fully take effect.  To save on client/server traffic, this
controller does not normally actively query the apiservers to find out
its previous actions; rather, this controller simply waits to be
informed through its Informers.  In the interim, this controller
maintains a record of actions in flight.  In particular, for each
address assignment in flight, the controller records: the
ResourceVersion of the NetworkAttachment that was seen to need an IP
address, the UID of the Subnet that was referenced when making the
assignment, the IP address assigned, and the ResourceVersion of the
NetworkAttachment created by the update that writes the assigned
address into the status of the attachment object. As long as the
Subnet's UID is unchanged and the attachment object's ResourceVersion
is one of the two recorded, the record is valid and retained.  However,
this does not work well enough, because the connection agents also
updates the NetworkAttachment objects, and the IP address controller
can be notified of such an update before being notified of the IP lock
object's creation.  To handle this possibility the IP address
controller will actively query for the lock object corresponding to
the IP address in a NetworkAttachment's Status if the controller does
not have that IP lock object in its informer's local cache.


## The Connection Agent

This is a controller that runs on each node and tells the local
networking implementation what it needs to know.

The most interesting problem faced by the connection agent is how to
stay informed about all the relevant NetworkAttachment objects and
none of the irrelevant ones.  A NetworkAttachment object X is relevant
to node N if and only if there exists a NetworkAttachment object Y
such that X and Y have the same VNI and Y is on node N.

The following approaches were considered, with the last one adopted.

- A connection agent has one Informer whose list&watch get all
  NetworkAttachment objects from the apiservers and filtering is done
  on the client side.  This was rejected because it can impose a LOT
  more load on the apiservers and agents than necessary.

- A connection agent has one Informer whose list&watch filter based on
  presence of a label specific to the agent's node, and there is a
  controller that adds the needed labels to the NetworkAttachment
  objects.  This was rejected because it involves a LOT of additional
  work to manage the labels.

- A connection agent has, at any given time, one Informer whose
  list&watch filter based on testing whether the NetworkAttachment's
  VNI value is in the set of currently relevant VNIs.  Whenever that
  set changes, the old Informer is stopped and a new one is created.
  This was rejected because the new Informer's initial list operation
  will produce largely redundant information, causing unnecessary load
  on the agents and the apiservers.

- A connection agent has, at any given time when the number of
  relevant VNIs is R, 1+R Informers on NetworkAttachments.  For one of
  those Informers, list&watch filter on whether `spec.node` identifies
  the agent's node.  Each of the other Informers is specific to a VNI,
  and that Informer's list&watch filter on whether `status.addressVNI`
  (which is the VNI where the attachment's locked address resides)
  equals the Informer's VNI.


## Test Driver

See [cmd/attachment-tput-driver](cmd/attachment-tput-driver) for a
test driver.


## The Kubernetes Cluster

KOS can be run on any standard Kubernetes cluster, provided you can
run privileged containers there and an adequate version of OVS can be
installed or found on each worker node.  Privileged containers are
used for the following.

- Provision data directories for Prometheus (if you deploy
  it using the option here).

- Define the CRD and cluster role for the etcd operator.

- Enable the connection agent to mount `/var/run/netns` with
  bidirectional mount propagation while using the host network
  namespace, which are hacks to make the ping testing performed
  by the test driver work (see
  [cmd/attachment-tput-driver](cmd/attachment-tput-driver) for more
  details).

KOS will use two subsets of the cluster, one for the KOS control plane
and one for KOS workload.  These subsets may be disjoint or overlap,
as appropriate for the cluster at hand.  These subsets are identified
by node labels.  The KOS control plane nodes are identified by the
node label `role.kos.example.com/control=true`, and the workload nodes
are identified by `role.kos.example.com/workload=true`.

You can get a list of your cluster's nodes along with their relevant
role labels with the following command.

```
kubectl get Node  -L role.kos.example.com/base,role.kos.example.com/control,role.kos.example.com/workload
```


## Operations Guide

### Introduction to Build and Deploy

This is only a simple example, and has an exceptionally simple
approach to building and deploying.  It supposes one Unix/Linux
machine, with connectivity to the Kube cluster's apiservers, for
building and operations.

There is a `Makefile` in the `kos` directory and it supports some of
the following steps.

### Dependencies

Dependencies are managed via [Glide](https://github.com/Masterminds/glide).

This example builds against release 1.14 of Kubernetes.

### Pre-Requisites

You will need the following installed on your build/ops machine.

- Go, release 1.12.1 or later

- Docker

- Glide

- make

- m4

- openssl, any non-embarrassing version

If you modify the API object type definitions then you will also need
the following.

- https://github.com/kubernetes/kube-openapi/tree/master/cmd/openapi-gen
  , built and on your `$PATH`.

With `kos` as your current working directory, `glide install -v` to
populate the `kos/vendor` directory.

On each worker machine in your Kube cluster you will need the
following.

- OVS (the `openvswitch-switch` package), release 2.3 or later

Before deploying KOS, the Kubernetes nodes must have the labels
discussed above to identify which KOS subset(s) each node belongs to.

### Prometheus and Grafana

An example of how to deploy and configure Prometheus and Grafana is in
`kos/metrics/`.  Deploy this with the following commands, with `kos` as
your current working directory.

```
kubectl apply  -f metrics/prometheus/manifests
kubectl create -f metrics/grafana/manifests
```

These restrict the Prometheus and Grafana servers to run on nodes
labeled `role.kos.example.com/base=true`, so you also need to label
some nodes like that.

The reason that `kubectl apply` is not used for Grafana is that the
configmap containing the dashboard definitions is too big.

This also creates a NodePort Service for the Prometheus server on node
port 30909, which means you can access the Prometheus server at
`http://workerNode:30909/` for any reachable `workerNode`.  Similarly,
this creates a NodePort Service for Grafana on node port 30003, so you
can reach Grafana at `http://workerNode:30003/`.

The source for the Prometheus config is actually in
`metrics/prometheus/config/config.yaml`.  If you edit that file then
invoke `metrics/prometheus/sync-configmap.sh` to update the configmap
template.

The sources for the Grafana dashboards are in
`metrics/grafana/{data*,dash*}`.  If you edit them (by hand or in
Grafana) then invoke `metrics/grafana/sync-configmaps.sh` to copy them
into the corresponding configmap templates.

The Prometheus configuration is based on
https://github.com/prometheus/prometheus/blob/63fe65bf2ff8c480bb4350e4d278d3208ca687be/documentation/examples/prometheus-kubernetes.yml
and has the following notable modifications.

- There is an additional Prometheus job, `kubernetes-etcd`, that will
  scrape `https://$node_internal_IP:2379/metrics` on nodes annotated
  with `prometheus.io/scrape-etcd=true`.  This call will use the TLS
  client credentials found in the secret named `main-etcd-client-tls`
  in the default namespace, expecting data items named `client.crt`
  and `client.key`.  This secret should exist _before_ Prometheus is
  deployed.  The server certificate is not checked.

- There is an additional Prometheus job, `kubernetes-nodes-cadvisor`,
  that will scrape port 4194 on nodes labelled
  `prometheus.io/scrape=true`.

- The `kubernetes-pods` job will scrape a pod if _either_

  - the pod is annotated `prometheus.io/scrape=true`, or
  - the pod is annotated `prometheus.io/sample=true` and the pod's
    node's entire name matches the regex
    `[0-9.]*|.*comp[0-9]*0|.*ctrl[0-9]*`.

- The `kubernetes-service-endpoints` job will scrape an endpoint if it
  is backed by a service and _either_

  - the service is annotated with `prometheus.io/scrape=true`, or
  - the service is annotated with `prometheus.io/sample=true` and the
    endpoint is based on a pod whose node's entire name matches the
    regex `[0-9.]*|.*comp[0-9]*0|.*ctrl[0-9]*`.

The connection agent gets annotated with `prometheus.io/sample=true`.

### Build

If you modify the API object type definitions in `pkg/apis/network`
then before proceeding with the rest of the build you must re-generate
the generated files.  To do that, invoke the `hack/update-codegen.sh`
script; this script takes no arguments and your current working
directory does not matter.

The main build work is done with `kos` as your current working
directory.  Do `make build`.  This creates binary executable files,
but not container images.

### Publish

With `kos` as your current working directory, `make publish`.  This
uses the existing executables to create container images and `docker
push` them.  The image for `$component` is pushed to
`${DOCKER_PREFIX}/kos-${component}:latest`, and `DOCKER_PREFIX`
defaults to `$LOGNAME` (i.e., the default is to push to the DockerHub
namespace that matches your login name) but you can override it in
your `make` command.  For example,`make publish
DOCKER_PREFIX=my.reg.com/solomon` would push to the `solomon`
namespace in the registry at `my.reg.com`.

Invoking `make publish` also specializes some deployment templates
with the proper container image references.  These are used in the
next step.

### Deploy

With `kos` as your current working directory and with `kubectl`
configured to manipulate your target Kubernetes cluster as a
privileged user (that is, set the KUBECONFIG environment variable or
your `~/.kube/config` file), `make deploy`.  This will instantiate
some template macros if necessary and then `kubectl apply` the various
files needed to deploy KOS.  If any macro-expanded files are missing
then this step references the `DOCKER_PREFIX` variable with the usual
default, so set it on your `make` command line if you are using a
different value.

The `make deploy` command also creates any missing parts of the TLS
certs and keys used to secure the etcd cluster.  This stuff is kept at
`${KOS_PERSIST}/etcd-tls/`, and the Makefile defaults `KOS_PERSIST` to
`${HOME}/.kos/`.

### Un-Deploy

With `kos` as your current working directory and with `kubectl`
configured to manipulate your target Kubernetes cluster as a
privileged user, `make undeploy`.  This will instantiate some template
macros if necessary and then `kubectl delete` everything that was
created in the `make deploy` step.  You need the `DOCKER_PREFIX`
variable set correctly as for `make deploy` and `make publish`, unless
the needed files already exist.

The `make undeploy` command, like `make deploy`, prepares any missing
YAML templates for the TLS secrets for the etcd cluster --- and thus
is also sensitive to the setting of `KOS_PERSIST`.
