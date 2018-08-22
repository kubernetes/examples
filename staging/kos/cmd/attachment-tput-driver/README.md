# attachment-tput-driver

This is a test driver that primarily exercises creation and deletion
of NetworkAttachment objects.  Subnet objects play a supporting role.

The driver uses informers on Subnet and NetworkAttachment objects to
monitor progress and performance.


## Virtual Network size distribution

For each run this driver creates a Kubernetes API namespace and puts
all the created objects in that namespace.  The namespace and every
object in it is labeled `app=attachment-tput-driver` for easy
identification.

This driver creates a set of Virtual Networks, whose sizes (i.e.,
number of "slots" --- see below) are given by a generalized power law
distribution.  There is currently no API object called a Virtual
Network.  Rather, a Virtual Network is simply identified by an integer
known as a Virtual Network Identifier (VNI).  In a given run the VNIs
are chosen sequentially (modulo 2^21), starting at one plus a randomly
chosen multiple of 65536.

This driver creates one or two subnets in each Virtual Network.  If
the Virtual Network's size is 1 or the `--single-subnet` flag is given
on the command line then the driver will create only one subnet for
the Virtual Network.  In all other cases two subnets are created and
the NetworkAttachments of that Virtual Network are alternately created
on the two subnets.

This driver creates no other API objects.

After creating the subnets the driver then waits to be notified of the
existence of all of them.  Once that is done, the driver then creates and
deletes NetworkAttachment objects in those subnets.

The NetworkAttachment objects are distributed among a given number of
Virtual Networks, with a generalized power law distribution of population.  The
parameters of the distribution are integers `top_vpc_size` and `bias`
and floating-point number `exponent`.  If the Virtual Networks are listed in
decreasing nominal number of attachments then the nominal population of
Virtual Network I (counting from I=1) is

```
ceiling( (1 + bias) * top_vpc_size / (I+bias)^exponent )
```

Setting `bias=0` and `exponent=1` gives a 1/I distribution --- but
these have a lot of Virtual Networks of size 1.  Other settings will
give more larger networks.  The driver will print out the total
nominal number of attachments (i.e., the sum of the nominal Virtual
Network sizes).  The driver also prints an important quantity that it
calls `avgPeers`.  If at a given moment each Virtual Network is at its
nominal size, and you pick a NetworkAttachment uniformly at random,
then `avgPeers` is the expected nominal size of that attachment's
Virtual Network.  Note that this is not simply the average of the
Virtual Network nominal sizes; rather, the large Virtual Networks
contribute more to `avgPeers`.

The driver produces a CSV file of (I, nominal size for I).  The driver
also produces an "outline" CSV file that omits the internal rows in a
run of constant nominal size.  The outline CSV file will have less
than `2*top_vpc_size` rows --- which might be much less than the
number of Virtual Networks, making the outline file easier to read and
plot.

If given the `--estimate` flag, the driver will not create any API
objects and will exit after printing Virtual Network size information
to stdout and those files.


## Timing and threading

This driver aims to issue requests to create and delete
NetworkAttachment objects steadily, at a given rate.  Once a
NetworkAttachment becomes "ready" it is normally tested with ping, but
this can optionally be disabled.

This driver issues NetworkAttachment creation and deletion requests
from a given number of threads.  The intended schedule of operations
for each thread is fixed by the driver's parameters.  The parameters
determine an intended time for each request to be issued.  If a thread
is ready to issue a request before its time then the thread will sleep
until the appointed time.  If a thread is not ready until after the
appointed time then the request will be issued as soon as the thread
can.

The timings of the threads are staggered.  That is, for a given
operation rate R, the first thread aims to issue its requests at
elapsed times `1/R`, `(1+num_threads)/R`, `(1+2*num_threads)/R`, ...;
the second thread aims to issue its requests at elapsed times `2/R`,
`(2+num_threads)/R`, `(2+2*num_threads)/R`, ...; and so on.

To minimize the number of non-full ping tests (see below), delays are
sometimes introduced into the time series described above.  If, at the
time an attachment is scheduled to be created, its virtual network has
no attachment that has successfully completed its test but _does_ have
some attachments that still might then delay will be introduced.  If
some attachment in the virtual network completes its test within a
certain limiting amount of time then the delay will be just long
enough so that the new attachment's test can use the newly tested
attachment as a peer; otherwise the delay will be that limiting amount
of time.  The delay limit is normally one minute but can be set by the
`--pending-wait` command line flag.


## Ping Testing

The driver normally exercises each NetworkAttachment once it becomes
ready, with `ping` operations.  This can be disabled with the
`--omit-test` command line flag.

The pings are done as follows. Each NetworkAttachment is directly
subjected to one test, which may indirectly test another attachment.
For a NetworkAttachment E that is created at a time when no existing
attachment in the same virtual network has successfully completed its
test, the test for E does the following:

- create a dedicated Linux network namespace on the attachment's host,

- move the attachment's Linux network interface into that network
  namespace,

- apply the proper IP configuration to that Linux network interface,

- use `ip addr` and `ip route` to display the configuration in the
  network namespace.

For an attachment that is created at a time when some existing
attachment of the virtual network has completed its test, the same
steps are done and then a series of `ping` operations is attempted.
The target IP of the pings is the IP address of the attachment in the
virtual network that most recently completed its test successfully.  A
number of `ping -c 1` are attempted, until one succeeds, up to a limit
which is normally 5 but can be overridden with the `--ping-count`
command line flag.

The driver requests a distinct network namespace for each attachment,
and for a given attachment requests that the network namespace be
deleted after the attachment is.

The attachment driver's metrics include histograms of create-to-tested
and ready-to-tested latencies, and counts of tests broken down by
result code and whether the test was "full".  The connection-agent's
metrics include histograms of test duration, and test counts broken
down by result code.

The mechanisms used to make the ping tests, and their cleanup, are two
scripts loaded in the connection-agent docker image. This is done through
instructions in the `Dockerfile`. These scripts are executed for a
NetworkAttachment if referenced in the `PostCreateExec` and `PostDeleteExec`
fields of its Spec. They run in a container, but they create a new network
namespace on the host and move a network interface on the host to such
namespace. To achieve this, the connection-agent container is less isolated
from the host than a normal container. More specifically:

- it runs on the host network namespace

- its `/var/run/netns` dir is the same as the host (through a bidirectional
  volume mount)

The first point ensures that the network interface is visible from within
the container. The second that the newly created namespace exists on the
host as well as the container.

## Node selection and Attachment Placement

The set of Nodes on which the driver will place attachments is normally
all of the cluster's Nodes.  The driver can optionally be given a
label selector (`--node-label-selector`) to filter the nodes.

The driver normally picks a node for each attachment uniformly at
random.  If given the `--round-robin` command line flag then the
driver will use that approach instead.


## IP and MAC addresses

The driver assigns disjoint CIDR blocks to the networks.  This
minimizes the additional infrastructure needed to test the data plane.
The IP blocks normally start at 172.24.0.0, and this can be overridden
with the `--base-address` flag.


## Warm-up, cruise, and cool-down phases

The driver is given a total number of network attachments to create.
This can be larger than the sum of the Virtual Network nominal sizes.  In
this case, the driver deletes NetworkAttachment objects when necessary
to keep each Virtual Network limited to its nominal size.

Each Virtual Network has a nominal number of attachments, as
described above; think of each Virtual Network as having this number of
_slots_.  The driver makes a fixed, randomized assignment of slots to
threads.  The number of slots given to one thread differs from the
number given to another thread by at most 1.

The total number of NetworkAttachment objects to create is also
distributed among the threads at the start, as evenly as possible.

At first each thread's slots are all empty.  A thread starts by
filling its slots --- i.e., issuing requests to create NetworkAttachment
objects.  Once all its slots are full, a thread enters the cruise
phase --- in which it has to delete one NetworkAttachment before
creating another.  Once a thread has created its planned number of
NetworkAttachments, the thread does nothing but delete its remaining
attachments.

One ^C will cause an early end to the cruise phase.  Upon receiving
the first ^C the driver stops creating NetworkAttachment objects;
subsequent delete operations are advanced as necessary to take the
places of any create operations thus forbidden.  The second ^C will
cause an abrupt termination.


## Shock testing

The driver can optionally deliver a shock test.  This means to have
only a warm-up phase, and to pause at the end of that phase to allow
notifications of status updates to be collected.  The
`--wait-after-create` flag turns on this behavior.


## Statistics collected

The driver collects several Prometheus statistics and makes them
available at `http://localhost:9101/metrics`.  At the end of a run,
the driver scrapes that to a file.

The driver uses an Informer to monitor the NetworkAttachments it
creates.  The driver notes when it is notified about a
NetworkAttachment getting an IP address, becoming "ready", going into
any bad state, or being tested.

For each create operation, and each delete operation, the driver takes
a `time.Now()` reading before and after making the call to do that
operation.  These operation latencies are noted in Prometheus
histograms, `attachment_create_latency_seconds` and
`attachment_delete_latency_seconds`.

The latencies from before-create to address-notification are collected
in the Prometheus histogram
`attachment_create_to_addressed_latency_seconds`.  The latencies from
before-create to ready-notification go in
`attachment_create_to_ready_latency_seconds`.  The latencies from
before-create to notification of a bad state are collected in
`attachment_create_to_broken_latency_seconds`.

The driver also produces counters of successful and failed create and
delete operations on NetworkAttachments: `successful_creates`,
`failed_creates`, `successful_deletes`, and `failed_deletes`.

At any given moment, the number of existing NetworkAttachment objects is
somewhere between `successful_creates - (successful_deletes +
failed_deletes)` and `successful_creates + failed_creates -
successful_deletes` (since the effect of a failed operation is not
clear).

The number of NetworkAttachment objects that never reached a ready nor
bad state is the difference between the sum of the counts in the
`attachment_create_to_ready_latency_seconds` and
`attachment_create_to_broken_latency_seconds` histograms and the number
of NetworkAttachment objects that were actually created.


## Error handling

If creation of the Kubernetes namespace or a KOS Subnet fails then the
error is logged and the driver exits.

If a given NetworkAttachment create operation fails then it is not
retried and its corresponding delete operation is skipped --- without
advancing the timing of the thread's subsequent operations.
Similarly, if a NetworkAttachment delete operation fails then it is
not retried.


## Output

The driver has two kinds of output: data files and `glog` logging.

For the data files, the driver creates a directory under the current
working directory.  The new directory's name is the same as the "run
ID" (which is checked for safety).  The driver writes three data files
into that directory.  One holds the result of scraping the driver's
Prometheus metrics.  The other two are the size distributions
described above, in CSV format.

For logging the driver produces a great volume of log messages with
the `glog` severity `INFO`, including every object creation and
deletion and every notification about interesting lifecycle events.
The driver does more circumspect logging with severities `WARNING` and
higher.  The driver also initializes the command line flag
`stderrthreshold` to `WARNING` before parsing the command line flags.
The net result of this is that, unless you tweak some relevant flags,
a reasonable set of log messages goes to stderr and all log messages
go to the INFO log file that `glog` produces (which goes in
`os.TempDir()`).

## Clean-up

The driver does not delete the Kubernetes API namespaces nor KOS
objects that it creates.  This is so that the experimenter can look at
whatever broken objects might remain.

Every API object created by the driver has the label
`app=attachment-tput-driver`.

A succinct way to clean up is to issue the command

```
kubectl delete Namespace -l app=attachment-tput-driver
```
