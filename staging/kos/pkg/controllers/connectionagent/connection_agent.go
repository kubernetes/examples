/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package connectionagent

import (
	"fmt"
	gonet "net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	k8scorev1api "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfields "k8s.io/apimachinery/pkg/fields"
	k8stypes "k8s.io/apimachinery/pkg/types"
	k8sutilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8swait "k8s.io/apimachinery/pkg/util/wait"
	k8scorev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	k8scache "k8s.io/client-go/tools/cache"
	k8seventrecord "k8s.io/client-go/tools/record"
	k8sworkqueue "k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	netv1a1 "k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
	kosclientset "k8s.io/examples/staging/kos/pkg/client/clientset/versioned"
	kosscheme "k8s.io/examples/staging/kos/pkg/client/clientset/versioned/scheme"
	netvifc1a1 "k8s.io/examples/staging/kos/pkg/client/clientset/versioned/typed/network/v1alpha1"
	kosinformers "k8s.io/examples/staging/kos/pkg/client/informers/externalversions"
	koslisterv1a1 "k8s.io/examples/staging/kos/pkg/client/listers/network/v1alpha1"
	netfabric "k8s.io/examples/staging/kos/pkg/networkfabric"
	"k8s.io/examples/staging/kos/pkg/util/parse"
)

// TODO update prometheus metrics
// TODO re-discuss with Mike change in MAC address handling
// TODO inteface to attachment handling need review and probably tweaks related to MAC/hostIP/ifc name

const (
	// localAttsCacheID is the ID for the local NetworkAttachments informer's
	// cache. Remote NetworkAttachments informers are partitioned by VNI so
	// their VNI can be used as the ID, but the local NetworkAttachments
	// informer is not associated to a single VNI. Pick 0 because it's not a
	// valid VNI value: there's no overlapping with the other IDs.
	localAttsCacheID cacheID = 0

	// Name of the indexer which computes a string concatenating the VNI and IP
	// of a network attachment. Used for syncing pre-existing interfaces at
	// start-up.
	attVNIAndIPIndexerName = "attachmentVNIAndIP"

	// NetworkAttachments in network.example.com/v1alpha1 fields names. Used to
	// build field selectors.
	attNodeField   = "spec.node"
	attIPv4Field   = "status.ipv4"
	attHostIPField = "status.hostIP"
	attVNIField    = "status.addressVNI"

	// resync period for Informers caches. Set
	// to 0 because we don't want resyncs.
	resyncPeriod = 0

	// netFabricRetryPeriod is the time we wait before retrying when a
	// network fabric operation fails while handling pre-existing interfaces.
	netFabricRetryPeriod = time.Second

	// The HTTP port under which the scraping endpoint ("/metrics") is served.
	// Pick an unusual one because the host's network namespace is used.
	// See https://github.com/prometheus/prometheus/wiki/Default-port-allocations .
	MetricsAddr = ":9294"

	// The HTTP path under which the scraping endpoint ("/metrics") is served.
	MetricsPath = "/metrics"

	// The namespace and subsystem of the Prometheus metrics produced here
	MetricsNamespace = "kos"
	MetricsSubsystem = "agent"
)

// cacheID models the ID of an informer's cache. Used to keep track of which
// informer's cache NetworkAttachments were seen in and should be looked up in.
type cacheID uint32

// virtualNetwork stores all the state needed for a virtual network for which
// there is at least one NetworkAttachment local to this connection agent.
type virtualNetwork struct {
	// remoteAttsInformer is an informer on the NetworkAttachments that are
	// both: (1) in this virtual network, (2) remote.
	// It is stopped by closing remoteAttsInformerStopCh when the last local
	// NetworkAttachment in this virtual network is deleted.
	remoteAttsInformer       k8scache.SharedIndexInformer
	remoteAttsInformerStopCh chan struct{}

	// remoteAttsLister is a lister on the NetworkAttachments that are both:
	// (1) in this virtual network, (2) remote.
	remoteAttsLister koslisterv1a1.NetworkAttachmentNamespaceLister

	// namespace is the K8s API namespace of this virtual network.
	namespace string

	// localAtts and remoteAtts store the names of the local and remote
	// NetworkAttachments in this virtual network, respectively. localAtts is
	// used to detect when the last local attachment is deleted. This signals
	// irrelevance of the virtual network for the connection agent. As a
	// consequence, remoteAttsInformer is stopped and remoteAtts is used to
	// enqueue references to remote NetworkAttachments so that their network
	// interfaces can be deleted.
	localAtts  map[string]struct{}
	remoteAtts map[string]struct{}
}

// ConnectionAgent represents a K8S controller which runs on every node of the
// cluster and eagerly maintains up-to-date the mapping between virtual IPs and
// host IPs for every relevant NetworkAttachment. A NetworkAttachment is
// relevant to a connection agent if: (1) it runs on the same node as the
// connection agent, or (2) it's in a virtual network where at least one
// NetworkAttachment for which (1) is true exists. To achieve its goal, a
// connection agent receives notifications about relevant NetworkAttachments
// through Informers, and when necessary creates/updates/deletes network
// interfaces through a low-level network interface fabric. When a new virtual
// network becomes relevant, a new informer on remote NetworkAttachments in that
// virtual network is created. Upon being notified of the creation of a local
// NetworkAttachment, the connection agent also updates the status of such
// attachment with its host IP and the name and the MAC address of the interface
// which was created.
type ConnectionAgent struct {
	nodeName      string
	hostIP        gonet.IP
	kcs           *kosclientset.Clientset
	netv1a1Ifc    netvifc1a1.NetworkV1alpha1Interface
	eventRecorder k8seventrecord.EventRecorder
	queue         k8sworkqueue.RateLimitingInterface
	workers       int
	netFabric     netfabric.Interface
	stopCh        <-chan struct{}

	// Informer and lister on NetworkAttachments on the same node as the
	// connection agent
	localAttsInformer k8scache.SharedIndexInformer
	localAttsLister   koslisterv1a1.NetworkAttachmentLister

	// Map from VNI to virtual network associated with that VNI.
	// Access only while holding vniToVirtNetMutex.
	// Never acquire vniToVirtNetMutex while holding attToSeenInCachesMutex, it
	// can lead to deadlock.
	vniToVirtNet      map[uint32]*virtualNetwork
	vniToVirtNetMutex sync.RWMutex

	// attToVirtNetVNI maps NetworkAttachments namespaced names to the VNI of
	// the virtualNetwork struct they're stored in.
	// Access only while holding attToVirtNetVNIMutex.
	attToVirtNetVNI      map[k8stypes.NamespacedName]uint32
	attToVirtNetVNIMutex sync.RWMutex

	// attToNetworkInterface maps NetworkAttachments namespaced names to their
	// network interfaces.
	// attToPostCreateExecReport maps local NetworkAttachments namespaced names
	// to the report of the command that was executed after creating their
	// network interface.
	// Access both only while holding attToNetworkInterfaceMutex.
	attToNetworkInterface      map[k8stypes.NamespacedName]networkInterface
	attToPostCreateExecReport  map[k8stypes.NamespacedName]*netv1a1.ExecReport
	attToNetworkInterfaceMutex sync.RWMutex

	// attToSeenInCaches maps NetworkAttachments namespaced names to the IDs
	// of the Informer's caches where they have been seen. It tells workers
	// where to look up attachments after dequeuing references and it's
	// populated by informers' notification handlers.
	// Access only while holding attToSeenInCachesMutex.
	attToSeenInCaches      map[k8stypes.NamespacedName]map[cacheID]struct{}
	attToSeenInCachesMutex sync.RWMutex

	// allowedPrograms is the values allowed to appear in the [0] of a
	// slice to exec post-create or -delete.
	allowedPrograms map[string]struct{}

	// NetworkAttachment.CreationTimestamp to local network interface creation latency
	attachmentCreateToLocalIfcHistogram prometheus.Histogram

	// NetworkAttachment.CreationTimestamp to remote network interface creation latency
	attachmentCreateToRemoteIfcHistogram prometheus.Histogram

	// Durations of calls on network fabric
	fabricLatencyHistograms *prometheus.HistogramVec

	// NetworkAttachment.CreationTimestamp to return from status update
	attachmentCreateToStatusHistogram prometheus.Histogram

	// round trip time for happy status update
	attachmentStatusHistograms *prometheus.HistogramVec

	localAttachmentsGauge  prometheus.Gauge
	remoteAttachmentsGauge prometheus.Gauge

	attachmentExecDurationHistograms *prometheus.HistogramVec
	attachmentExecStatusCounts       *prometheus.CounterVec
}

// New returns a deactivated instance of a ConnectionAgent (neither the workers
// goroutines nor any Informer have been started). Invoke Run to activate.
func New(nodeName string,
	hostIP gonet.IP,
	kcs *kosclientset.Clientset,
	eventIfc k8scorev1client.EventInterface,
	queue k8sworkqueue.RateLimitingInterface,
	workers int,
	netFabric netfabric.Interface,
	allowedPrograms map[string]struct{}) *ConnectionAgent {

	attachmentCreateToLocalIfcHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "attachment_create_to_local_ifc_latency_seconds",
			Help:        "Seconds from attachment CreationTimestamp to finished creating local interface",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": nodeName},
		})
	attachmentCreateToRemoteIfcHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "attachment_create_to_remote_ifc_latency_seconds",
			Help:        "Seconds from attachment CreationTimestamp to finished creating remote interface",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": nodeName},
		})
	fabricLatencyHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "fabric_latency_seconds",
			Help:        "Network fabric operation time in seconds",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16},
			ConstLabels: map[string]string{"node": nodeName},
		},
		[]string{"op", "err"})
	attachmentCreateToStatusHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "attachment_create_to_status_latency_seconds",
			Help:        "Seconds from attachment CreationTimestamp to return from successful status update",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": nodeName},
		})
	attachmentStatusHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "attachment_status_latency_seconds",
			Help:        "Round trip latency to update attachment status, in seconds",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": nodeName},
		},
		[]string{"statusErr", "err"})
	localAttachmentsGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "local_attachments",
			Help:        "Number of local attachments in network fabric",
			ConstLabels: map[string]string{"node": nodeName},
		})
	remoteAttachmentsGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "remote_attachments",
			Help:        "Number of remote attachments in network fabric",
			ConstLabels: map[string]string{"node": nodeName},
		})
	attachmentExecDurationHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "attachment_exec_duration_secs",
			Help:        "Time to run attachment commands, in seconds",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": nodeName},
		},
		[]string{"what"})
	attachmentExecStatusCounts := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "attachment_exec_status_count",
			Help:        "Counts of commands by what and exit status",
			ConstLabels: map[string]string{"node": nodeName},
		},
		[]string{"what", "exitStatus"})
	fabricNameCounts := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "fabric_count",
			Help:        "Indicator of chosen fabric implementation",
			ConstLabels: map[string]string{"node": nodeName},
		},
		[]string{"fabric"})
	workerCount := prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "worker_count",
			Help:        "Number of queue worker threads",
			ConstLabels: map[string]string{"node": nodeName},
		})
	prometheus.MustRegister(attachmentCreateToLocalIfcHistogram, attachmentCreateToRemoteIfcHistogram, fabricLatencyHistograms, attachmentCreateToStatusHistogram, attachmentStatusHistograms, localAttachmentsGauge, remoteAttachmentsGauge, attachmentExecDurationHistograms, attachmentExecStatusCounts, fabricNameCounts, workerCount)

	fabricNameCounts.With(prometheus.Labels{"fabric": netFabric.Name()}).Inc()
	workerCount.Add(float64(workers))
	eventBroadcaster := k8seventrecord.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.V(3).Infof)
	eventBroadcaster.StartRecordingToSink(&k8scorev1client.EventSinkImpl{eventIfc})
	eventRecorder := eventBroadcaster.NewRecorder(kosscheme.Scheme, k8scorev1api.EventSource{Component: "connection-agent", Host: nodeName})

	return &ConnectionAgent{
		nodeName:                             nodeName,
		hostIP:                               hostIP,
		kcs:                                  kcs,
		netv1a1Ifc:                           kcs.NetworkV1alpha1(),
		eventRecorder:                        eventRecorder,
		queue:                                queue,
		workers:                              workers,
		netFabric:                            netFabric,
		vniToVirtNet:                         make(map[uint32]*virtualNetwork),
		attToVirtNetVNI:                      make(map[k8stypes.NamespacedName]uint32),
		attToNetworkInterface:                make(map[k8stypes.NamespacedName]networkInterface),
		attToPostCreateExecReport:            make(map[k8stypes.NamespacedName]*netv1a1.ExecReport),
		attToSeenInCaches:                    make(map[k8stypes.NamespacedName]map[cacheID]struct{}),
		allowedPrograms:                      allowedPrograms,
		attachmentCreateToLocalIfcHistogram:  attachmentCreateToLocalIfcHistogram,
		attachmentCreateToRemoteIfcHistogram: attachmentCreateToRemoteIfcHistogram,
		fabricLatencyHistograms:              fabricLatencyHistograms,
		attachmentCreateToStatusHistogram:    attachmentCreateToStatusHistogram,
		attachmentStatusHistograms:           attachmentStatusHistograms,
		localAttachmentsGauge:                localAttachmentsGauge,
		remoteAttachmentsGauge:               remoteAttachmentsGauge,
		attachmentExecDurationHistograms:     attachmentExecDurationHistograms,
		attachmentExecStatusCounts:           attachmentExecStatusCounts,
	}
}

// Run activates the ConnectionAgent: the local attachments informer is started,
// pre-existing network interfaces on the node are synced, and the worker
// goroutines are started. Close stopCh to stop the ConnectionAgent.
func (ca *ConnectionAgent) Run(stopCh <-chan struct{}) error {
	defer k8sutilruntime.HandleCrash()
	defer ca.queue.ShutDown()

	// Serve Prometheus metrics
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		klog.Errorf("In-process HTTP server crashed: %s", http.ListenAndServe(MetricsAddr, nil).Error())
	}()

	ca.stopCh = stopCh

	ca.initLocalAttsInformerAndLister()
	go ca.localAttsInformer.Run(stopCh)
	klog.V(2).Infoln("Local NetworkAttachments informer started")

	if !k8scache.WaitForCacheSync(stopCh, ca.localAttsInformer.HasSynced) {
		return fmt.Errorf("Local NetworkAttachments informer failed to sync")
	}
	klog.V(2).Infoln("Local NetworkAttachments informer synced")

	if err := ca.syncPreExistingIfcs(); err != nil {
		return err
	}
	klog.V(2).Infoln("Pre-existing interfaces synced")

	for i := 0; i < ca.workers; i++ {
		go k8swait.Until(ca.processQueue, time.Second, stopCh)
	}
	klog.V(2).Infof("Launched %d workers", ca.workers)

	<-stopCh
	return nil
}

func (ca *ConnectionAgent) initLocalAttsInformerAndLister() {
	ca.localAttsInformer, ca.localAttsLister = ca.newInformerAndLister(resyncPeriod, k8smetav1.NamespaceAll, ca.localAttSelector())

	ca.localAttsInformer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
		AddFunc:    ca.onLocalAttAdd,
		UpdateFunc: ca.onLocalAttUpdate,
		DeleteFunc: ca.onLocalAttDelete})
}

func (ca *ConnectionAgent) onLocalAttAdd(obj interface{}) {
	att := obj.(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Local NetworkAttachments cache: notified of addition of %#+v", att)
	attNSN := parse.AttNSN(att)
	ca.addSeenInCache(attNSN, localAttsCacheID)
	ca.queue.Add(attNSN)
}

func (ca *ConnectionAgent) onLocalAttUpdate(oldObj, obj interface{}) {
	oldAtt, att := oldObj.(*netv1a1.NetworkAttachment), obj.(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Local NetworkAttachments cache: notified of update from %#+v to %#+v", oldAtt, att)

	// Enqueue if the UID changed because if a local NetworkAttachment is
	// deleted and replaced the status.hostIP field of the newer attachment is
	// set to "", and the connection agent has to write back the correct value.
	// Also, the only fields affecting local network interfaces handling that
	// can be seen changing by this function are status.ipv4 and
	// status.addressVNI, so enqueue if they changed.
	if oldAtt.UID != att.UID || oldAtt.Status.IPv4 != att.Status.IPv4 || oldAtt.Status.AddressVNI != att.Status.AddressVNI {
		ca.queue.Add(parse.AttNSN(att))
	}
}

func (ca *ConnectionAgent) onLocalAttDelete(obj interface{}) {
	att := parse.Peel(obj).(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Local NetworkAttachments cache: notified of removal of %#+v", att)
	attNSN := parse.AttNSN(att)
	ca.removeSeenInCache(attNSN, localAttsCacheID)
	ca.queue.Add(attNSN)
}

func (ca *ConnectionAgent) syncPreExistingIfcs() error {
	if err := ca.syncPreExistingLocalIfcs(); err != nil {
		return err
	}

	return ca.syncPreExistingRemoteIfcs()
}

func (ca *ConnectionAgent) syncPreExistingLocalIfcs() error {
	// TODO: implement
	return nil
}

func (ca *ConnectionAgent) syncPreExistingRemoteIfcs() error {
	// TODO: implement
	return nil
}

func (ca *ConnectionAgent) processQueue() {
	for {
		item, stop := ca.queue.Get()
		if stop {
			return
		}
		attNSN := item.(k8stypes.NamespacedName)
		ca.processQueueItem(attNSN)
	}
}

func (ca *ConnectionAgent) processQueueItem(attNSN k8stypes.NamespacedName) {
	defer ca.queue.Done(attNSN)
	requeues := ca.queue.NumRequeues(attNSN)
	klog.V(5).Infof("Working on attachment %s, with %d earlier requeues", attNSN, requeues)
	err := ca.processNetworkAttachment(attNSN)
	if err != nil {
		klog.Warningf("Failed processing NetworkAttachment %s, requeuing (%d earlier requeues): %s", attNSN, requeues, err.Error())
		ca.queue.AddRateLimited(attNSN)
		return
	}
	klog.V(4).Infof("Finished NetworkAttachment %s with %d requeues", attNSN, requeues)
	ca.queue.Forget(attNSN)
}

func (ca *ConnectionAgent) processNetworkAttachment(attNSN k8stypes.NamespacedName) error {
	att, haltProcessing := ca.getNetworkAttachment(attNSN)
	if haltProcessing {
		return nil
	}

	// Even if the NetworkAttachment exists (att != nil) we might find out that
	// its virtual network has become irrelevant, and the NetworkAttachment
	// should therefore be treated as a deleted one (att = nil). If that's the
	// case syncVirtualNetwork will return nil, hence we write the return arg to
	// att.
	att = ca.syncVirtualNetwork(attNSN, att)

	attIfc, statusErrs, postCreateExecReport, err := ca.syncNetworkInterface(attNSN, att)
	if err != nil {
		return err
	}

	// The only thing left to do is updating the NetworkAttachment status. If
	// it's not needed, return.
	if att == nil || ca.nodeName != att.Spec.Node {
		return nil
	}
	// If we're here there's no doubt that the NetworkAttachment is local, and
	// so is its network interface.
	localIfc := attIfc.(*localNetworkInterface)
	ifcMAC := localIfc.GuestMAC.String()
	if ca.localAttachmentIsUpToDate(att, ifcMAC, localIfc.Name, statusErrs, postCreateExecReport) {
		return nil
	}

	return ca.updateLocalAttachmentStatus(att, ifcMAC, localIfc.Name, statusErrs, postCreateExecReport)
}

// getNetworkAttachment attempts to determine the univocal version of the
// NetworkAttachment with namespaced name `attNSN`. If it succeeds it returns
// the attachment (nil if it was deleted). The second return argument tells
// clients whether they should stop working on the NetworkAttachment. It is set
// to true if an unexpected error occurs or if the current state of the
// NetworkAttachment cannot be unambiguously determined.
func (ca *ConnectionAgent) getNetworkAttachment(attNSN k8stypes.NamespacedName) (att *netv1a1.NetworkAttachment, haltProcessing bool) {
	// Get the ID of the cache where the NetworkAttachment was seen. There
	// could be more than one.
	attCacheID, nbrOfSeenInCaches := ca.getSeenInCache(attNSN)

	if nbrOfSeenInCaches > 1 {
		// If the NetworkAttachment was seen in more than one informer's cache
		// the most up-to-date version is unkown. Halt processing until future
		// delete notifications from the informers storing stale versions arrive
		// and reveal the current state of the NetworkAttachment.
		klog.V(4).Infof("Cannot process NetworkAttachment %s because it was seen in more than one informer.", attNSN)
		haltProcessing = true
		return
	}

	// If the NetworkAttachment has been seen only in one informer's cache, get
	// the lister backed by that cache so that we can retrieve the
	// NetworkAttachment.
	var attCache koslisterv1a1.NetworkAttachmentNamespaceLister
	if nbrOfSeenInCaches == 1 {
		if attCacheID == localAttsCacheID {
			attCache = ca.localAttsLister.NetworkAttachments(attNSN.Namespace)
		} else {
			attCache = ca.getRemoteAttsLister(uint32(attCacheID))
		}
	}

	if attCache == nil {
		// Either the NetworkAttachment was seen in no informer's cache because
		// it was deleted, or the lister backed by the cache where it was seen
		// has not been found because the attachment is remote and its virtual
		// network has become irrelevant making the attachment itself
		// irrelevant. As far as the connection agent is concerned the
		// NetworkAttachment has been deleted.
		return
	}

	// Retrieve the NetworkAttachment.
	att, err := attCache.Get(attNSN.Name)
	if err != nil && !k8serrors.IsNotFound(err) {
		klog.Errorf("Failed to look up NetworkAttachment %s: %s. This should never happen, there will be no retry.", attNSN, err.Error())
		haltProcessing = true
	}

	return
}

func (ca *ConnectionAgent) localAttachmentIsUpToDate(att *netv1a1.NetworkAttachment, macAddr, ifcName string, statusErrs sliceOfString, postCreateER *netv1a1.ExecReport) bool {
	return macAddr == att.Status.MACAddress &&
		ifcName == att.Status.IfcName &&
		ca.hostIP.String() == att.Status.HostIP &&
		statusErrs.Equal(att.Status.Errors.Host) &&
		postCreateER.Equiv(att.Status.PostCreateExecReport)
}

func (ca *ConnectionAgent) syncVirtualNetwork(attNSN k8stypes.NamespacedName, att *netv1a1.NetworkAttachment) *netv1a1.NetworkAttachment {
	oldVNI, oldVNIFound := ca.getVirtNetVNI(attNSN)
	if oldVNIFound && att == nil || att.Status.AddressVNI != oldVNI {
		// Remove the NetworkAttachment from its old virtual network.
		ca.updateOldVirtualNetwork(attNSN, oldVNI)
	}

	if att != nil {
		// Add the NetworkAttachment to its current virtual network.
		// Even if the NetworkAttachment exists (att != nil) we might find out
		// that its virtual network has become irrelevant, and the
		// NetworkAttachment should therefore be treated as a deleted one
		// (att = nil). If that's the case set att to nil before returning it so
		// that the caller knows.
		att, _ = ca.updateVirtualNetwork(att)
	}

	return att
}

// updateVirtualNetwork adds a NetworkAttachment to its virtualNetwork.
// It is safe to call even if the NetworkAttachment is already part of the
// virtualNetwork state. Edge cases where the NetworkAttachment location (local
// or remote) has changed since the last invocation are handled.
// It assumes that `att` is non-nil.
// Normally the first return arg is `att` itself, but if the virtual network has
// become irrelevant it returns nil to signal to the caller that the
// NetworkAttachment should be treated as a deleted one.
// The second return arg is the updated virtualNetwork.
func (ca *ConnectionAgent) updateVirtualNetwork(att *netv1a1.NetworkAttachment) (*netv1a1.NetworkAttachment, *virtualNetwork) {
	attNSN := parse.AttNSN(att)

	ca.vniToVirtNetMutex.Lock()
	defer func() {
		ca.vniToVirtNetMutex.Unlock()
		if att == nil {
			ca.clearVirtNetVNI(attNSN)
			return
		}
		ca.setVirtNetVNI(attNSN, att.Status.AddressVNI)
	}()

	vn := ca.vniToVirtNet[att.Status.AddressVNI]
	if vn == nil {
		if att.Spec.Node != ca.nodeName {
			// The NetworkAttachment is remote and has become irrelevant because
			// the last local NetworkAttachment in its virtual network has been
			// deleted.
			att = nil
			return att, nil
		}

		// The NetworkAttachment is the first local one with its VNI: its
		// virtual network has just become relevant, initialize the needed state.
		vn = ca.initVirtualNetwork(att.Namespace, att.Status.AddressVNI)
		ca.vniToVirtNet[att.Status.AddressVNI] = vn
		klog.V(3).Infof("Creation of %s made virtual network with VNI %d relevant. Its state has been initialized.", attNSN, att.Status.AddressVNI)
	}

	if att.Spec.Node == ca.nodeName {
		// In case there was a deleted remote NetworkAttachment with the same
		// namespaced name as this one in the virtual network, remove it.
		delete(vn.remoteAtts, att.Name)

		vn.localAtts[att.Name] = struct{}{}
		return att, vn
	}

	// The NetworkAttachment is remote, but maybe there was a deleted local
	// NetworkAttachment with the same namespaced name as this one in the
	// virtual network. Remove it and check whether it was the last local
	// NetworkAttachment: if that's the case the virtual network has become
	// irrelevant.
	delete(vn.localAtts, att.Name)
	if len(vn.localAtts) == 0 {
		// The virtual network has become irrelevant.
		delete(ca.vniToVirtNet, att.Status.AddressVNI)
		ca.finalizeVirtualNetwork(vn, att.Status.AddressVNI)
		klog.V(3).Infof("Deletion of %s made virtual network with VNI %d irrelevant. All the state associated with it is being deleted.", attNSN, att.Status.AddressVNI)
		att = nil
		return att, nil
	}

	vn.remoteAtts[att.Name] = struct{}{}

	return att, vn
}

// updateOldVirtualNetwork removes attNSN from the virtualNetwork associated
// with oldVNI and performs additional clean up (such as clearing the
// virtualNetwork) if needed.
func (ca *ConnectionAgent) updateOldVirtualNetwork(attNSN k8stypes.NamespacedName, oldVNI uint32) {
	ca.vniToVirtNetMutex.Lock()
	defer func() {
		ca.vniToVirtNetMutex.Unlock()
		ca.clearVirtNetVNI(attNSN)
	}()

	oldVN := ca.vniToVirtNet[oldVNI]
	if oldVN == nil {
		return
	}

	delete(oldVN.localAtts, attNSN.Name)
	if len(oldVN.localAtts) == 0 {
		delete(ca.vniToVirtNet, oldVNI)
		ca.finalizeVirtualNetwork(oldVN, oldVNI)
		klog.V(3).Infof("Deletion of %s made virtual network with VNI %d irrelevant. All the state associated with it is being deleted.", attNSN, oldVNI)
		return
	}

	delete(oldVN.remoteAtts, attNSN.Name)
}

func (ca *ConnectionAgent) syncNetworkInterface(attNSN k8stypes.NamespacedName, att *netv1a1.NetworkAttachment) (ifc networkInterface, statusErrs sliceOfString, postCreateER *netv1a1.ExecReport, err error) {
	oldIfc, oldExecReport, oldIfcFound := ca.getNetworkInterface(attNSN)
	oldIfcCanBeUsed := oldIfcFound && oldIfc.canBeOwnedBy(att)

	if oldIfcFound && !oldIfcCanBeUsed {
		err = ca.deleteNetworkInterface(attNSN, oldIfc)
		if err != nil {
			return
		}
	}

	if att == nil {
		return
	}

	if oldIfcCanBeUsed {
		statusErrs = ca.LaunchCommand(attNSN, oldIfc, att.Spec.PostCreateExec, "postCreate", false, false)
		postCreateER = oldExecReport
		ifc = oldIfc
		return
	}

	ifc, statusErrs, err = ca.createNetworkInterface(att)
	return
}

func (ca *ConnectionAgent) updateLocalAttachmentStatus(att *netv1a1.NetworkAttachment, macAddr, ifcName string, statusErrs sliceOfString, pcer *netv1a1.ExecReport) error {
	att2 := att.DeepCopy()
	att2.Status.MACAddress = macAddr
	att2.Status.IfcName = ifcName
	att2.Status.HostIP = ca.hostIP.String()
	att2.Status.Errors.Host = statusErrs
	att2.Status.PostCreateExecReport = pcer

	tBeforeUpdate := time.Now()
	updatedAtt, err := ca.netv1a1Ifc.NetworkAttachments(att2.Namespace).Update(att2)
	tAfterUpdate := time.Now()

	ca.attachmentStatusHistograms.With(prometheus.Labels{"statusErr": formatErrVal(len(statusErrs) > 0), "err": formatErrVal(err != nil)}).Observe(tAfterUpdate.Sub(tBeforeUpdate).Seconds())

	if err != nil {
		return fmt.Errorf("status update with RV=%s, ipv4=%s, hostIP=%s, macAddress=%s, ifcName=%s, statusErrs=%#+v, PostCreateExecReport=%#+v failed: %s",
			att.ResourceVersion,
			att.Status.IPv4,
			ca.hostIP,
			macAddr,
			ifcName,
			statusErrs,
			pcer,
			err.Error())
	}

	klog.V(3).Infof("Updated NetworkAttachment %s's status: oldRV=%s, newRV=%s, ipv4=%s, hostIP=%s, macAddress=%s, ifcName=%s, statusErrs=%#+v, PostCreateExecReport=%#+v",
		parse.AttNSN(att),
		att.ResourceVersion,
		updatedAtt.ResourceVersion,
		updatedAtt.Status.IPv4,
		updatedAtt.Status.HostIP,
		updatedAtt.Status.MACAddress,
		updatedAtt.Status.IfcName,
		updatedAtt.Status.Errors.Host,
		updatedAtt.Status.PostCreateExecReport)

	if att.Status.HostIP == "" {
		ca.attachmentCreateToStatusHistogram.Observe(tAfterUpdate.Truncate(time.Second).Sub(att.CreationTimestamp.Time).Seconds())
	}

	return nil
}

// finalizeVirtualNetwork stops the informer on remote attachments on the
// virtual network and enqueues references to such attachments so that their
// interfaces can be deleted.
func (ca *ConnectionAgent) finalizeVirtualNetwork(vn *virtualNetwork, vni uint32) {
	close(vn.remoteAttsInformerStopCh)

	for remAtt := range vn.remoteAtts {
		nsn := k8stypes.NamespacedName{Namespace: vn.namespace,
			Name: remAtt}
		ca.removeSeenInCache(nsn, cacheID(vni))
		ca.queue.Add(nsn)
	}
}

func (ca *ConnectionAgent) initVirtualNetwork(ns string, vni uint32) *virtualNetwork {
	remoteAttsInformer, remoteAttsLister := ca.newInformerAndLister(resyncPeriod, ns, ca.remoteAttSelector(vni))

	remoteAttsInformer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
		AddFunc:    ca.onRemoteAttAdd,
		UpdateFunc: ca.onRemoteAttUpdate,
		DeleteFunc: ca.onRemoteAttDelete})

	remoteAttsInformerStopCh := make(chan struct{})
	go remoteAttsInformer.Run(mergeStopChannels(ca.stopCh, remoteAttsInformerStopCh))

	return &virtualNetwork{
		remoteAttsInformer:       remoteAttsInformer,
		remoteAttsInformerStopCh: remoteAttsInformerStopCh,
		remoteAttsLister:         remoteAttsLister.NetworkAttachments(ns),
		namespace:                ns,
		localAtts:                make(map[string]struct{}),
		remoteAtts:               make(map[string]struct{}),
	}
}

func (ca *ConnectionAgent) onRemoteAttAdd(obj interface{}) {
	att := obj.(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Remote NetworkAttachments cache for VNI %06x: notified of addition of %#+v", att.Status.AddressVNI, att)
	attNSN := parse.AttNSN(att)
	ca.addSeenInCache(attNSN, cacheID(att.Status.AddressVNI))
	ca.queue.Add(attNSN)
}

func (ca *ConnectionAgent) onRemoteAttUpdate(oldObj, obj interface{}) {
	oldAtt, att := oldObj.(*netv1a1.NetworkAttachment), obj.(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Remote NetworkAttachments cache for VNI %06x: notified of update from %#+v to %#+v.", att.Status.AddressVNI, oldAtt, att)

	// The only fields affecting remote network interfaces handling that can be
	// seen changing by this function are status.ipv4 and status.hostIP, so
	// enqueue only if they changed.
	if oldAtt.Status.IPv4 != att.Status.IPv4 || oldAtt.Status.HostIP != att.Status.HostIP {
		ca.queue.Add(parse.AttNSN(att))
	}
}

func (ca *ConnectionAgent) onRemoteAttDelete(obj interface{}) {
	att := parse.Peel(obj).(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Remote NetworkAttachments cache for VNI %06x: notified of deletion of %#+v", att.Status.AddressVNI, att)
	attNSN := parse.AttNSN(att)
	ca.removeSeenInCache(attNSN, cacheID(att.Status.AddressVNI))
	ca.queue.Add(attNSN)
}

func (ca *ConnectionAgent) getNetworkInterface(att k8stypes.NamespacedName) (ifc networkInterface, postCreateER *netv1a1.ExecReport, ifcFound bool) {
	ca.attToNetworkInterfaceMutex.RLock()
	defer ca.attToNetworkInterfaceMutex.RUnlock()

	ifc, ifcFound = ca.attToNetworkInterface[att]
	if ifcFound {
		postCreateER = ca.attToPostCreateExecReport[att]
	}
	return
}

func (ca *ConnectionAgent) assignNetworkInterface(att k8stypes.NamespacedName, ifc networkInterface) {
	ca.attToNetworkInterfaceMutex.Lock()
	defer ca.attToNetworkInterfaceMutex.Unlock()

	ca.attToNetworkInterface[att] = ifc
	// TODO: update gauge for local/remote NetworkAttachments.
}

func (ca *ConnectionAgent) setExecReport(att k8stypes.NamespacedName, ifcID string, er *netv1a1.ExecReport) {
	ca.attToNetworkInterfaceMutex.Lock()
	defer ca.attToNetworkInterfaceMutex.Unlock()

	ifc, ifcFound := ca.attToNetworkInterface[att]
	if ifcFound {
		// Post create execs are bound to a specific interface but run
		// asynchronously wrt to the workers that process NetworkAttachments and
		// create/delete network interfaces. The following checks make sure that
		// we don't set the exec report on a network interface different than
		// that the exec report is bound to.
		localIfc, isLocal := ifc.(*localNetworkInterface)
		if isLocal && localIfc.id == ifcID {
			ca.attToPostCreateExecReport[att] = er
		}
	}
}

func (ca *ConnectionAgent) unassignNetworkInterface(att k8stypes.NamespacedName) {
	ca.attToNetworkInterfaceMutex.Lock()
	defer ca.attToNetworkInterfaceMutex.Unlock()

	delete(ca.attToNetworkInterface, att)
	delete(ca.attToPostCreateExecReport, att)
	// TODO: update gauge for local/remote NetworkAttachments.
}

func (ca *ConnectionAgent) getVirtNetVNI(att k8stypes.NamespacedName) (vni uint32, found bool) {
	ca.attToVirtNetVNIMutex.RLock()
	defer ca.attToVirtNetVNIMutex.RUnlock()

	vni, found = ca.attToVirtNetVNI[att]
	return
}

func (ca *ConnectionAgent) clearVirtNetVNI(att k8stypes.NamespacedName) {
	ca.attToVirtNetVNIMutex.Lock()
	defer ca.attToVirtNetVNIMutex.Unlock()

	delete(ca.attToVirtNetVNI, att)
}

func (ca *ConnectionAgent) setVirtNetVNI(att k8stypes.NamespacedName, vni uint32) {
	ca.attToVirtNetVNIMutex.Lock()
	defer ca.attToVirtNetVNIMutex.Unlock()

	ca.attToVirtNetVNI[att] = vni
}

func (ca *ConnectionAgent) addSeenInCache(att k8stypes.NamespacedName, cache cacheID) {
	ca.attToSeenInCachesMutex.Lock()
	defer ca.attToSeenInCachesMutex.Unlock()

	seenInCaches := ca.attToSeenInCaches[att]
	if seenInCaches == nil {
		seenInCaches = make(map[cacheID]struct{}, 1)
		ca.attToSeenInCaches[att] = seenInCaches
	}

	seenInCaches[cache] = struct{}{}
}

func (ca *ConnectionAgent) removeSeenInCache(att k8stypes.NamespacedName, cache cacheID) {
	ca.attToSeenInCachesMutex.Lock()
	defer ca.attToSeenInCachesMutex.Unlock()

	seenInCaches := ca.attToSeenInCaches[att]
	if seenInCaches == nil {
		return
	}

	delete(seenInCaches, cache)
	if len(seenInCaches) == 0 {
		delete(ca.attToSeenInCaches, att)
	}
}

func (ca *ConnectionAgent) getSeenInCache(att k8stypes.NamespacedName) (seenInCache cacheID, nbrOfSeenInCaches int) {
	ca.attToSeenInCachesMutex.RLock()
	defer ca.attToSeenInCachesMutex.RUnlock()

	seenInCaches := ca.attToSeenInCaches[att]
	nbrOfSeenInCaches = len(seenInCaches)
	if nbrOfSeenInCaches == 1 {
		for seenInCache = range seenInCaches {
		}
	}

	return
}

func (ca *ConnectionAgent) getRemoteAttsLister(vni uint32) koslisterv1a1.NetworkAttachmentNamespaceLister {
	ca.vniToVirtNetMutex.RLock()
	defer ca.vniToVirtNetMutex.RUnlock()

	vn := ca.vniToVirtNet[vni]
	if vn == nil {
		return nil
	}

	return vn.remoteAttsLister
}

// localAttSelector returns a fields selector that matches local
// NetworkAttachments for whom a network interface can be created.
func (ca *ConnectionAgent) localAttSelector() fieldsSelector {
	// The NetworkAttachment must be local.
	localAtt := k8sfields.OneTermEqualSelector(attNodeField, ca.nodeName)

	// The NetworkAttachment must have a virtual IP to create a network
	// interface.
	attWithAnIP := k8sfields.OneTermNotEqualSelector(attIPv4Field, "")

	// Return a selector given by the logical AND between localAtt and
	// attWithAnIP.
	return fieldsSelector{k8sfields.AndSelectors(localAtt, attWithAnIP)}
}

// remoteAttSelector returns a fields selector that matches remote
// NetworkAttachments in the virtual network identified by `vni` for whom a
// network interface can be created.
func (ca *ConnectionAgent) remoteAttSelector(vni uint32) fieldsSelector {
	// The NetworkAttachment must be remote.
	remoteAtt := k8sfields.OneTermNotEqualSelector(attNodeField, ca.nodeName)

	// The NetworkAttachment must be in the Virtual Network identified by vni.
	attInSpecificVN := k8sfields.OneTermEqualSelector(attVNIField, strconv.FormatUint(uint64(vni), 10))

	// The NetworkAttachment must have a virtual IP.
	attWithAnIP := k8sfields.OneTermNotEqualSelector(attIPv4Field, "")

	// The NetworkAttachment's host IP must be known so that packets can be sent
	// to that host.
	attWithHostIP := k8sfields.OneTermNotEqualSelector(attHostIPField, "")

	// Return a selector given by the logical AND between all the selectors
	// defined above.
	return fieldsSelector{k8sfields.AndSelectors(remoteAtt, attInSpecificVN, attWithAnIP, attWithHostIP)}
}

func (ca *ConnectionAgent) newInformerAndLister(resyncPeriod time.Duration, ns string, fs fieldsSelector) (k8scache.SharedIndexInformer, koslisterv1a1.NetworkAttachmentLister) {
	tloFunc := fs.toTweakListOptionsFunc()
	networkAttachments := kosinformers.NewFilteredSharedInformerFactory(ca.kcs, resyncPeriod, ns, tloFunc).Network().V1alpha1().NetworkAttachments()

	// Add indexer used at start up to match pre-existing network interfaces to
	// owning NetworkAttachment (if one exists).
	networkAttachments.Informer().AddIndexers(map[string]k8scache.IndexFunc{attVNIAndIPIndexerName: attVNIAndIPIndexer})

	return networkAttachments.Informer(), networkAttachments.Lister()
}

// attVNIAndIPIndexer is an Index function that computes a string made up by vni
// and IP of a NetworkAttachment. Used to sync pre-existing interfaces with
// attachments at start up.
func attVNIAndIPIndexer(obj interface{}) ([]string, error) {
	att := obj.(*netv1a1.NetworkAttachment)
	return []string{attVNIAndIP(att.Status.AddressVNI, att.Status.IPv4)}, nil
}
