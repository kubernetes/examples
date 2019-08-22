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

// vnState stores all the state needed for a Virtual Network for
// which there is at least one NetworkAttachment local to this node.
type vnState struct {
	// remoteAttsInformer is an informer on the NetworkAttachments that are
	// both: (1) in the Virtual Network the vnState represents, (2) not on
	// this node. It is stopped when the last local NetworkAttachment in the
	// Virtual Network associated with the vnState instance is deleted. To
	// stop it, remoteAttsInformerStopCh must be closed.
	remoteAttsInformer       k8scache.SharedIndexInformer
	remoteAttsInformerStopCh chan struct{}

	// remoteAttsLister is a lister on the NetworkAttachments that are
	// both: (1) in the Virtual Network the vnState represents, (2) not
	// on this node. Since a Virtual Network cannot span multiple k8s API
	// namespaces, it's a NamespaceLister.
	remoteAttsLister koslisterv1a1.NetworkAttachmentNamespaceLister

	// namespace is the namespace of the Virtual Network
	// associated with this vnState.
	namespace string

	// localAtts and remoteAtts store the names of the local and remote
	// NetworkAttachments in the Virtual Network the vnState represents,
	// respectively. localAtts is used to detect when the last local attachment
	// in the virtual network is deleted, so that remoteAttsInformer can be
	// stopped. remoteAtts is used to enqueue references to the remote
	// attachments in the Virtual Network when such Virtual Network becomes
	// irrelevant (deletion of last local attachment), so that the interfaces of
	// the remote attachments can be deleted.
	localAtts  map[string]struct{}
	remoteAtts map[string]struct{}
}

// ConnectionAgent represents a K8S controller which runs on every node of the
// cluster and eagerly maintains up-to-date the mapping between virtual IPs and
// physical IPs for every relevant NetworkAttachment. A NetworkAttachment is
// relevant to a connection agent if: (1) it runs on the same node as the
// connection agent, or (2) it's part of a Virtual Network where at least one
// NetworkAttachment for which (1) is true exists. To achieve its goal, a
// connection agent receives notifications about relevant NetworkAttachments
// from the K8s API server through Informers, and when necessary
// creates/updates/deletes Network Interfaces through a low-level network
// interface fabric. When a new Virtual Network becomes relevant for the
// connection agent because of the creation of the first attachment of that
// Virtual Network on the same node as the connection agent, a new informer on
// remote NetworkAttachments in that Virtual Network is created. Upon being
// notified of the creation of a local NetworkAttachment, the connection agent
// also updates the status of such attachment with its host IP and the name of
// the interface which was created.
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

	// Map from vni to vnState associated with that vni. Accessed only while
	// holding vniToVnStateMutex
	vniToVnState      map[uint32]*vnState
	vniToVnStateMutex sync.RWMutex

	// nsnToVNStateVNI maps local attachments namespaced names to the VNI of the
	// vnState they're stored in. Accessed only while holding nsnToVNStateVNIMutex.
	nsnToVNStateVNI      map[k8stypes.NamespacedName]uint32
	nsnToVNStateVNIMutex sync.RWMutex

	nsnToLocalMainState       map[k8stypes.NamespacedName]LocalAttachmentMainState
	nsnToPostCreateExecReport map[k8stypes.NamespacedName]*netv1a1.ExecReport
	nsnToLocalStateMutex      sync.RWMutex

	nsnToRemoteIfc      map[k8stypes.NamespacedName]netfabric.RemoteNetIfc
	nsnToRemoteIfcMutex sync.RWMutex

	// nsnToSeenInCaches maps NetworkAttachments namespaced names to the IDs
	// of the Informer's caches where they have been seen. It tells workers
	// where to look up attachments after dequeuing references and it's
	// populated by informers' notification handlers.
	// Access only while holding nsnToSeenInCachesMutex.
	nsnToSeenInCaches      map[k8stypes.NamespacedName]map[cacheID]struct{}
	nsnToSeenInCachesMutex sync.RWMutex

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

// LocalAttachmentMainState is the state in this agent for a local
// NetworkAttachment that is implicitly locked by worker threads.
type LocalAttachmentMainState struct {
	netfabric.LocalNetIfc
	PostDeleteExec []string
}

// LocalAttachmentState is all the state in this agent for a local
// NetworkAttachment.
type LocalAttachmentState struct {
	LocalAttachmentMainState
	PostCreateExecReport *netv1a1.ExecReport
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
		vniToVnState:                         make(map[uint32]*vnState),
		nsnToVNStateVNI:                      make(map[k8stypes.NamespacedName]uint32),
		nsnToLocalMainState:                  make(map[k8stypes.NamespacedName]LocalAttachmentMainState),
		nsnToPostCreateExecReport:            make(map[k8stypes.NamespacedName]*netv1a1.ExecReport),
		nsnToRemoteIfc:                       make(map[k8stypes.NamespacedName]netfabric.RemoteNetIfc),
		nsnToSeenInCaches:                    make(map[k8stypes.NamespacedName]map[cacheID]struct{}),
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
		DeleteFunc: ca.onLocalAttDelete,
	})
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
		// If we're here there's been an error: either the attachment current state was
		// ambiguous (e.g. more than one vni), or there's been a problem while processing
		// it (e.g. Interface creation failed). We requeue the attachment reference so that
		// it can be processed again and hopefully next time there will be no errors.
		klog.Warningf("Failed processing NetworkAttachment %s, requeuing (%d earlier requeues): %s",
			attNSN,
			requeues,
			err.Error())
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

	if att != nil {
		// The NetworkAttachment exists and it's current state is univocal.
		return ca.processExistingAtt(att)
	}

	// The NetworkAttachment has been deleted.
	return ca.processDeletedAtt(attNSN)
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

func (ca *ConnectionAgent) processExistingAtt(att *netv1a1.NetworkAttachment) error {
	attNSN, attVNI := parse.AttNSN(att), att.Status.AddressVNI
	attNode := att.Spec.Node
	klog.V(5).Infof("Working on existing: attachment=%s, node=%s, resourceVersion=%s", attNSN, attNode, att.ResourceVersion)

	// Update the vnState associated with the attachment. This typically involves
	// adding the attachment to the vnState associated to its vni (and initializing
	// that vnState if the attachment is the first local one with its vni), but
	// could also entail removing the attachment from the vnState associated with
	// its old vni if the vni has changed.
	vnState, noVnStateFoundForRemoteAtt := ca.updateVNState(attVNI, attNSN, attNode)
	if vnState != nil {
		// If we're here att is currently remote but was previously the last local
		// attachment in its vni. Thus, we act as if the last local attachment
		// in the vn was deleted
		ca.clearVNResources(vnState, attNSN.Name, attVNI)
		return nil
	}
	if noVnStateFoundForRemoteAtt {
		// If we're here att is remote but its vnState has been removed because
		// of the deletion of the last local attachment in its virtual network
		// between the lookup in the remote attachments cache and the attempt to
		// set the attachment name into its vnState, hence we treat it as a deleted
		// attachment.
		ca.removeSeenInCache(attNSN, cacheID(attVNI))
		return ca.processDeletedAtt(attNSN)
	}

	// Create or update the interface associated with the attachment.
	var attHostIP gonet.IP
	if attNode == ca.nodeName {
		attHostIP = ca.hostIP
	} else {
		attHostIP = gonet.ParseIP(att.Status.HostIP)
	}
	attGuestIP := gonet.ParseIP(att.Status.IPv4)
	macAddrS, ifcName, statusErrs, pcer, err := ca.createOrUpdateIfc(attGuestIP,
		attHostIP,
		attVNI,
		att)
	if err != nil {
		return err
	}

	// If the attachment is not local then we are done.
	if attNode != ca.nodeName {
		return nil
	}
	// The attachment is local, so make sure its status reflects the
	// state held here
	localHostIPStr := ca.hostIP.String()
	if localHostIPStr != att.Status.HostIP ||
		ifcName != att.Status.IfcName ||
		macAddrS != att.Status.MACAddress ||
		!pcer.Equiv(att.Status.PostCreateExecReport) ||
		!statusErrs.Equal(sliceOfString(att.Status.Errors.Host)) {

		klog.V(5).Infof("Attempting update of %s from resourceVersion=%s because host IP %q != %q or ifcName %q != %q or MAC address %q != %q or PCER %#+v != %#+v or statusErrs %#+v != %#+v", attNSN, att.ResourceVersion, localHostIPStr, att.Status.HostIP, ifcName, att.Status.IfcName, macAddrS, att.Status.MACAddress, pcer, att.Status.PostCreateExecReport, statusErrs, att.Status.Errors.Host)
		updatedAtt, err := ca.setAttStatus(att, macAddrS, ifcName, statusErrs, pcer)
		if err != nil {
			klog.V(3).Infof("Failed to update att %s status: oldRV=%s, ipv4=%s, macAddress=%s, ifcName=%s, PostCreateExecReport=%#+v",
				attNSN,
				att.ResourceVersion,
				att.Status.IPv4,
				macAddrS,
				ifcName,
				pcer)
			return err
		}
		klog.V(3).Infof("Updated att %s status: oldRV=%s, newRV=%s, ipv4=%s, hostIP=%s, macAddress=%s, ifcName=%s, statusErrs=%#+v, PostCreateExecReport=%#+v",
			attNSN,
			att.ResourceVersion,
			updatedAtt.ResourceVersion,
			updatedAtt.Status.IPv4,
			updatedAtt.Status.HostIP,
			updatedAtt.Status.MACAddress,
			updatedAtt.Status.IfcName,
			updatedAtt.Status.Errors.Host,
			updatedAtt.Status.PostCreateExecReport)
	}

	return nil
}

func (ca *ConnectionAgent) processDeletedAtt(attNSN k8stypes.NamespacedName) error {
	vnStateVNI, vnStateVNIFound := ca.getVNStateVNI(attNSN)
	if vnStateVNIFound {
		ca.updateVNStateAfterAttDeparture(attNSN.Name, vnStateVNI)
		ca.unsetVNStateVNI(attNSN)
	}

	localState, attHasLocalIfc := ca.getLocalAttState(attNSN)
	if attHasLocalIfc {
		if err := ca.DeleteLocalIfc(localState.LocalNetIfc); err != nil {
			return err
		}
		ca.LaunchCommand(attNSN, &localState.LocalNetIfc, localState.PostDeleteExec, "postDelete", true, false)
		ca.unsetLocalAttState(attNSN)
		return nil
	}

	remoteIfc, attHasRemoteIfc := ca.getRemoteIfc(attNSN)
	if attHasRemoteIfc {
		if err := ca.DeleteRemoteIfc(remoteIfc); err != nil {
			return err
		}
		ca.unsetRemoteIfc(attNSN)
	}

	return nil
}

func (ca *ConnectionAgent) updateVNState(attNewVNI uint32,
	attNSN k8stypes.NamespacedName,
	attNode string) (*vnState, bool) {

	attOldVNI, oldVNIFound := ca.getVNStateVNI(attNSN)
	if oldVNIFound && attOldVNI != attNewVNI {
		// if we're here the attachment vni changed since the last time it
		// was processed, hence we update the vnState associated with the
		// old value of the vni to reflect the attachment departure.
		ca.updateVNStateAfterAttDeparture(attNSN.Name, attOldVNI)
		ca.unsetVNStateVNI(attNSN)
	}

	return ca.updateVNStateForExistingAtt(attNSN, attNode == ca.nodeName, attNewVNI)
}

// updateVNStateForExistingAtt adds the attachment to the vnState associated with
// its vni. If the attachment is local and is the first one for its vni, the
// associated vnState is initialized (this entails starting the remote attachments
// informer). If the attachment was the last local attachment in its vnState and
// has become remote, the vnState for its vni is cleared (it's removed from the
// map storing the vnStates) and returned, so that the caller can perform a clean
// up of the resources associated with the vnState (remote attachments informer
// is stopped and references to the remote attachments are enqueued). If the
// attachment is remote and its vnState cannot be found (because the last local
// attachment in the same Virtual Network has been deleted) noVnStateFoundForRemoteAtt
// is set to true so that the caller knows and can react appropriately.
func (ca *ConnectionAgent) updateVNStateForExistingAtt(attNSN k8stypes.NamespacedName,
	attIsLocal bool,
	vni uint32) (vnStateRet *vnState, noVnStateFoundForRemoteAtt bool) {

	attName := attNSN.Name
	firstLocalAttInVN := false

	ca.vniToVnStateMutex.Lock()
	defer func() {
		ca.vniToVnStateMutex.Unlock()
		if vnStateRet == nil && !noVnStateFoundForRemoteAtt {
			ca.setVNStateVNI(attNSN, vni)
		} else {
			ca.unsetVNStateVNI(attNSN)
		}
		if firstLocalAttInVN {
			klog.V(2).Infof("VN with ID %06x became relevant: an Informer has been started", vni)
		}
	}()

	vnState := ca.vniToVnState[vni]
	if attIsLocal {
		// If the vnState for the attachment vni is missing it means that the
		// attachment is the first local one for its vni, hence we initialize
		// the vnState (this entails starting the remote attachments informer).
		// We also add the attachment name to the local attachments in the
		// virtual network and remove the attachment name from the remote
		// attachments: this is needed in case we're here because of a delete
		// followed by a re-create which did not change the vni but made the
		// attachment transition from remote to local.
		if vnState == nil {
			vnState = ca.initVNState(vni, attNSN.Namespace)
			ca.vniToVnState[vni] = vnState
			firstLocalAttInVN = true
		}
		delete(vnState.remoteAtts, attName)
		vnState.localAtts[attName] = struct{}{}
	} else {
		// If the vnState for the attachment's vni is not missing (because the
		// last local attachment with the same vni has been deleted), we add the
		// attachment name to the remote attachments in the vnState. Then we
		// remove the attachment name from the local attachments: this is needed
		// in case we're here because of a delete followed by a re-create which
		// did not change the vni but made the attachment transition from local
		// to remote. After doing this, we check whether the local attachment
		// we've removed was the last one for its vni. If that's the case, the
		// vni is no longer relevant to the connection agent, hence we unset and
		// return the vnState so that the caller can perform the necessary clean
		// up. If the vnState is missing, we set the return flag
		// noVnStateFoundForRemoteAtt to true so that the caller knows that the
		// remote attachment it is processing must not get an interface (this is
		// needed because a reference to such attachment is not necessarily
		// already in the list of remote attachments in the vn, hence it's not
		// granted that such a reference has been enqueued to delete the
		// attachment).
		if vnState != nil {
			vnState.remoteAtts[attName] = struct{}{}
			delete(vnState.localAtts, attName)
			if len(vnState.localAtts) == 0 {
				delete(ca.vniToVnState, vni)
				vnStateRet = vnState
			}
		} else {
			noVnStateFoundForRemoteAtt = true
		}
	}

	return
}

func (ca *ConnectionAgent) updateVNStateAfterAttDeparture(attName string, vni uint32) {
	vnState := ca.removeAttFromVNState(attName, vni)
	if vnState == nil {
		return
	}
	// If we're here attName was the last local attachment in the virtual network
	// with id vni. Hence we stop the remote attachments informer and enqueue
	// references to remote attachments in that virtual network, so that their
	// interfaces can be deleted.
	ca.clearVNResources(vnState, attName, vni)
}

// createOrUpdateIfc returns the MAC address, Linux interface name,
// post-create exec report, and possibly an error
func (ca *ConnectionAgent) createOrUpdateIfc(attGuestIP, attHostIP gonet.IP, attVNI uint32, att *netv1a1.NetworkAttachment) (attMACStr, ifcName string, statusErrs sliceOfString, pcer *netv1a1.ExecReport, err error) {
	attNSN := parse.AttNSN(att)
	attMAC := generateMACAddr(attVNI, attGuestIP)
	attMACStr = attMAC.String()
	ifcName = generateIfcName(attMAC)
	oldLocalState, attHasLocalIfc := ca.getLocalAttState(attNSN)
	oldRemoteIfc, attHasRemoteIfc := ca.getRemoteIfc(attNSN)
	newIfcNeedsToBeCreated := (!attHasLocalIfc && !attHasRemoteIfc) ||
		(attHasLocalIfc && ifcNeedsUpdate(ca.hostIP, attHostIP, oldLocalState.GuestMAC, attMAC)) ||
		(attHasRemoteIfc && ifcNeedsUpdate(oldRemoteIfc.HostIP, attHostIP, oldRemoteIfc.GuestMAC, attMAC))

	if newIfcNeedsToBeCreated {
		if attHasLocalIfc {
			if err := ca.DeleteLocalIfc(oldLocalState.LocalNetIfc); err != nil {
				return "", "", nil, nil, fmt.Errorf("update of network interface of attachment %s failed, old local interface %#+v could not be deleted: %s",
					attNSN,
					oldLocalState.LocalNetIfc,
					err.Error())
			}
			ca.LaunchCommand(attNSN, &oldLocalState.LocalNetIfc, oldLocalState.PostDeleteExec, "postDelete", true, false)
			ca.unsetLocalAttState(attNSN)
		}
		if attHasRemoteIfc {
			if err := ca.DeleteRemoteIfc(oldRemoteIfc); err != nil {
				return "", "", nil, nil, fmt.Errorf("update of network interface of attachment %s failed, old remote interface %#+v could not be deleted: %s",
					attNSN,
					oldRemoteIfc,
					err.Error())
			}
			ca.unsetRemoteIfc(attNSN)
		}

		if attHostIP.Equal(ca.hostIP) {
			newLocalState := LocalAttachmentMainState{
				LocalNetIfc: netfabric.LocalNetIfc{
					Name:     ifcName,
					VNI:      attVNI,
					GuestMAC: attMAC,
					GuestIP:  attGuestIP,
				},
				PostDeleteExec: att.Spec.PostDeleteExec,
			}
			tBeforeFabric := time.Now()
			err := ca.netFabric.CreateLocalIfc(newLocalState.LocalNetIfc)
			tAfterFabric := time.Now()
			ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "CreateLocalIfc", "err": formatErrVal(err != nil)}).Observe(tAfterFabric.Sub(tBeforeFabric).Seconds())
			if err != nil {
				return "", "", nil, nil, fmt.Errorf("creation of local network interface of attachment %s failed, interface %#+v could not be created: %s",
					attNSN,
					newLocalState.LocalNetIfc,
					err.Error())
			}
			ca.attachmentCreateToLocalIfcHistogram.Observe(tAfterFabric.Truncate(time.Second).Sub(att.CreationTimestamp.Time).Seconds())
			ca.setLocalAttMainState(attNSN, newLocalState)
			ca.eventRecorder.Eventf(att, k8scorev1api.EventTypeNormal, "Implemented", "Created Linux network interface named %s with MAC address %s and IPv4 address %s", newLocalState.Name, newLocalState.GuestMAC, newLocalState.GuestIP)
			statusErrs = ca.LaunchCommand(attNSN, &newLocalState.LocalNetIfc, att.Spec.PostCreateExec, "postCreateExec", true, true)
		} else {
			newRemoteIfc := netfabric.RemoteNetIfc{
				VNI:      attVNI,
				GuestMAC: attMAC,
				GuestIP:  attGuestIP,
				HostIP:   attHostIP,
			}
			tBefore := time.Now()
			err := ca.netFabric.CreateRemoteIfc(newRemoteIfc)
			tAfter := time.Now()
			ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "CreateRemoteIfc", "err": formatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
			if err != nil {
				return "", "", nil, nil, fmt.Errorf("creation of remote network interface of attachment %s failed, interface %#+v could not be created: %s",
					attNSN,
					newRemoteIfc,
					err.Error())
			}
			ca.attachmentCreateToRemoteIfcHistogram.Observe(tAfter.Truncate(time.Second).Sub(att.CreationTimestamp.Time).Seconds())
			ca.assignRemoteIfc(attNSN, newRemoteIfc)
		}
	} else if attHasLocalIfc {
		statusErrs = ca.LaunchCommand(attNSN, &oldLocalState.LocalNetIfc, att.Spec.PostCreateExec, "postCreateExec", false, true)
		pcer = oldLocalState.PostCreateExecReport
	}

	return
}

func (ca *ConnectionAgent) setAttStatus(att *netv1a1.NetworkAttachment, macAddrS, ifcName string, statusErrs sliceOfString, pcer *netv1a1.ExecReport) (*netv1a1.NetworkAttachment, error) {
	att2 := att.DeepCopy()
	att2.Status.Errors.Host = statusErrs
	att2.Status.MACAddress = macAddrS
	att2.Status.HostIP = ca.hostIP.String()
	att2.Status.IfcName = ifcName
	att2.Status.PostCreateExecReport = pcer
	tBeforeStatus := time.Now()
	updatedAtt, err := ca.netv1a1Ifc.NetworkAttachments(att2.Namespace).Update(att2)
	tAfterStatus := time.Now()
	ca.attachmentStatusHistograms.With(prometheus.Labels{"statusErr": formatErrVal(len(statusErrs) > 0), "err": formatErrVal(err != nil)}).Observe(tAfterStatus.Sub(tBeforeStatus).Seconds())
	if err == nil && att.Status.IfcName == "" {
		ca.attachmentCreateToStatusHistogram.Observe(tAfterStatus.Truncate(time.Second).Sub(att.CreationTimestamp.Time).Seconds())
	}
	return updatedAtt, err
}

// removeAttFromVNState removes attName from the vnState associated with vni, both
// for local and remote attachments. If attName is the last local attachment in
// the vnState, vnState is returned, so that the caller can perform additional
// clean up (e.g. stopping the remote attachments informer).
func (ca *ConnectionAgent) removeAttFromVNState(attName string, vni uint32) *vnState {
	ca.vniToVnStateMutex.Lock()
	defer ca.vniToVnStateMutex.Unlock()
	vnState := ca.vniToVnState[vni]
	if vnState != nil {
		delete(vnState.localAtts, attName)
		if len(vnState.localAtts) == 0 {
			delete(ca.vniToVnState, vni)
			return vnState
		}
		delete(vnState.remoteAtts, attName)
	}
	return nil
}

// clearVNResources stops the informer on remote attachments on the virtual
// network and enqueues references to such attachments so that their interfaces
// can be deleted.
func (ca *ConnectionAgent) clearVNResources(vnState *vnState, lastAttName string, vni uint32) {
	close(vnState.remoteAttsInformerStopCh)
	klog.V(2).Infof("NetworkAttachment %s/%s was the last local with vni %06x: remote attachments informer was stopped",
		vnState.namespace,
		lastAttName,
		vni)

	for aRemoteAttName := range vnState.remoteAtts {
		aRemoteAttNSN := k8stypes.NamespacedName{
			Namespace: vnState.namespace,
			Name:      aRemoteAttName,
		}
		ca.removeSeenInCache(aRemoteAttNSN, cacheID(vni))
		ca.queue.Add(aRemoteAttNSN)
	}
}

func (ca *ConnectionAgent) initVNState(vni uint32, namespace string) *vnState {
	remoteAttsInformer, remoteAttsLister := ca.newInformerAndLister(resyncPeriod, namespace, ca.remoteAttSelector(vni))

	remoteAttsInformer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
		AddFunc:    ca.onRemoteAttAdd,
		UpdateFunc: ca.onRemoteAttUpdate,
		DeleteFunc: ca.onRemoteAttDelete,
	})

	remoteAttsInformerStopCh := make(chan struct{})
	go remoteAttsInformer.Run(mergeStopChannels(ca.stopCh, remoteAttsInformerStopCh))

	return &vnState{
		remoteAttsInformer:       remoteAttsInformer,
		remoteAttsInformerStopCh: remoteAttsInformerStopCh,
		remoteAttsLister:         remoteAttsLister.NetworkAttachments(namespace),
		namespace:                namespace,
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

func (ca *ConnectionAgent) getLocalAttState(nsn k8stypes.NamespacedName) (state LocalAttachmentState, ifcFound bool) {
	ca.nsnToLocalStateMutex.RLock()
	defer ca.nsnToLocalStateMutex.RUnlock()
	state.LocalAttachmentMainState, ifcFound = ca.nsnToLocalMainState[nsn]
	if ifcFound {
		state.PostCreateExecReport = ca.nsnToPostCreateExecReport[nsn]
	}
	return
}

func (ca *ConnectionAgent) setLocalAttMainState(nsn k8stypes.NamespacedName, state LocalAttachmentMainState) {
	ca.nsnToLocalStateMutex.Lock()
	defer ca.nsnToLocalStateMutex.Unlock()
	ca.nsnToLocalMainState[nsn] = state
	ca.localAttachmentsGauge.Set(float64(len(ca.nsnToLocalMainState)))
}

func (ca *ConnectionAgent) setExecReport(nsn k8stypes.NamespacedName, er *netv1a1.ExecReport) {
	ca.nsnToLocalStateMutex.Lock()
	defer ca.nsnToLocalStateMutex.Unlock()
	_, ok := ca.nsnToLocalMainState[nsn]
	if ok {
		ca.nsnToPostCreateExecReport[nsn] = er
	}
}

func (ca *ConnectionAgent) getRemoteIfc(nsn k8stypes.NamespacedName) (ifc netfabric.RemoteNetIfc, ifcFound bool) {
	ca.nsnToRemoteIfcMutex.RLock()
	defer ca.nsnToRemoteIfcMutex.RUnlock()
	ifc, ifcFound = ca.nsnToRemoteIfc[nsn]
	return
}

func (ca *ConnectionAgent) assignRemoteIfc(nsn k8stypes.NamespacedName, ifc netfabric.RemoteNetIfc) {
	ca.nsnToRemoteIfcMutex.Lock()
	defer ca.nsnToRemoteIfcMutex.Unlock()
	ca.nsnToRemoteIfc[nsn] = ifc
	ca.remoteAttachmentsGauge.Set(float64(len(ca.nsnToRemoteIfc)))
}

func (ca *ConnectionAgent) getVNStateVNI(nsn k8stypes.NamespacedName) (vni uint32, vniFound bool) {
	ca.nsnToVNStateVNIMutex.RLock()
	defer ca.nsnToVNStateVNIMutex.RUnlock()
	vni, vniFound = ca.nsnToVNStateVNI[nsn]
	return
}

func (ca *ConnectionAgent) unsetVNStateVNI(nsn k8stypes.NamespacedName) {
	ca.nsnToVNStateVNIMutex.Lock()
	defer ca.nsnToVNStateVNIMutex.Unlock()
	delete(ca.nsnToVNStateVNI, nsn)
}

func (ca *ConnectionAgent) setVNStateVNI(nsn k8stypes.NamespacedName, vni uint32) {
	ca.nsnToVNStateVNIMutex.Lock()
	defer ca.nsnToVNStateVNIMutex.Unlock()
	ca.nsnToVNStateVNI[nsn] = vni
}

func (ca *ConnectionAgent) unsetLocalAttState(nsn k8stypes.NamespacedName) {
	ca.nsnToLocalStateMutex.Lock()
	defer ca.nsnToLocalStateMutex.Unlock()
	delete(ca.nsnToLocalMainState, nsn)
	delete(ca.nsnToPostCreateExecReport, nsn)
	ca.localAttachmentsGauge.Set(float64(len(ca.nsnToLocalMainState)))
}

func (ca *ConnectionAgent) unsetRemoteIfc(nsn k8stypes.NamespacedName) {
	ca.nsnToRemoteIfcMutex.Lock()
	defer ca.nsnToRemoteIfcMutex.Unlock()
	delete(ca.nsnToRemoteIfc, nsn)
	ca.remoteAttachmentsGauge.Set(float64(len(ca.nsnToRemoteIfc)))
}

func (ca *ConnectionAgent) addSeenInCache(nsn k8stypes.NamespacedName, cache cacheID) {
	ca.nsnToSeenInCachesMutex.Lock()
	defer ca.nsnToSeenInCachesMutex.Unlock()

	seenInCaches := ca.nsnToSeenInCaches[nsn]
	if seenInCaches == nil {
		seenInCaches = make(map[cacheID]struct{}, 1)
		ca.nsnToSeenInCaches[nsn] = seenInCaches
	}

	seenInCaches[cache] = struct{}{}
}

func (ca *ConnectionAgent) removeSeenInCache(nsn k8stypes.NamespacedName, cache cacheID) {
	ca.nsnToSeenInCachesMutex.Lock()
	defer ca.nsnToSeenInCachesMutex.Unlock()

	seenInCaches := ca.nsnToSeenInCaches[nsn]
	if seenInCaches == nil {
		return
	}

	delete(seenInCaches, cache)
	if len(seenInCaches) == 0 {
		delete(ca.nsnToSeenInCaches, nsn)
	}
}

func (ca *ConnectionAgent) getSeenInCache(nsn k8stypes.NamespacedName) (seenInCache cacheID, nbrOfSeenInCaches int) {
	ca.nsnToSeenInCachesMutex.RLock()
	defer ca.nsnToSeenInCachesMutex.RUnlock()

	seenInCaches := ca.nsnToSeenInCaches[nsn]
	nbrOfSeenInCaches = len(seenInCaches)
	if nbrOfSeenInCaches == 1 {
		for seenInCache = range seenInCaches {
		}
	}

	return
}

func (ca *ConnectionAgent) getRemoteAttsLister(vni uint32) koslisterv1a1.NetworkAttachmentNamespaceLister {
	ca.vniToVnStateMutex.RLock()
	defer ca.vniToVnStateMutex.RUnlock()

	vnState := ca.vniToVnState[vni]
	if vnState == nil {
		return nil
	}

	return vnState.remoteAttsLister
}

// getRemoteAttsIndexerForVNI accesses the map with all the vnStates but it's not
// thread-safe because it is meant to be used only at start-up, when there's only
// one goroutine running.
func (ca *ConnectionAgent) getRemoteAttsInformerForVNI(vni uint32) (k8scache.SharedIndexInformer, chan struct{}) {
	vnState := ca.vniToVnState[vni]
	if vnState == nil {
		return nil, nil
	}
	return vnState.remoteAttsInformer, vnState.remoteAttsInformerStopCh
}

// localAttSelector returns a fields selector that matches local
// NetworkAttachments for whom a network interface can be created.
func (ca *ConnectionAgent) localAttSelector() fieldsSelector {
	// The NetworkAttachment must be local.
	localAtt := k8sfields.OneTermEqualSelector(attNodeField, ca.nodeName)

	// The NetworkAttachment must have a virtual IP to create an Interface.
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
	// owning NetworkAttachment.
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

func (ca *ConnectionAgent) DeleteLocalIfc(ifc netfabric.LocalNetIfc) error {
	tBefore := time.Now()
	err := ca.netFabric.DeleteLocalIfc(ifc)
	tAfter := time.Now()
	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "DeleteLocalIfc", "err": formatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
	return err
}

func (ca *ConnectionAgent) DeleteRemoteIfc(ifc netfabric.RemoteNetIfc) error {
	tBefore := time.Now()
	err := ca.netFabric.DeleteRemoteIfc(ifc)
	tAfter := time.Now()
	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "DeleteRemoteIfc", "err": formatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
	return err
}
