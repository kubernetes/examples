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
	k8slabels "k8s.io/apimachinery/pkg/labels"
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

	// Name of the indexer used to match pre-existing network interfaces to
	// network attachments.
	ifcOwnerDataIndexerName = "ifcOwnerData"

	// Field names of NetworkAttachments used to build field selectors.
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

	// The HTTP port at which Prometheus metrics are served.
	// Pick an unusual one because the host's network namespace is used.
	// See https://github.com/prometheus/prometheus/wiki/Default-port-allocations .
	metricsAddr = ":9294"

	// The HTTP path at which Prometheus metrics are served.
	metricsPath = "/metrics"

	// The namespace and subsystem of the Prometheus metrics produced here
	metricsNamespace = "kos"
	metricsSubsystem = "agent"
)

// cacheID models the ID of an informer's cache. Used to keep track of which
// informer's cache NetworkAttachments were seen in and should be looked up in.
type cacheID uint32

// layer1VirtualNetworkState is the first layer of state associated with a
// single relevant virtual network.
type layer1VirtualNetworkState struct {
	// unique identifier over a run of the connection agent of this
	// layer1VirtualNetworkState. Used to prevent race conditions.
	uid uint64

	// List of names of remote NetworkAttachments in the virtual network (which
	// implicitly determines the namespace) for whom an add notification handler
	// has been executed but a delete notification handler has not.
	remoteAtts map[string]struct{}

	// Lister used by workers to retrieve the NetworkAttachment they're
	// processing.
	remoteAttsLister koslisterv1a1.NetworkAttachmentNamespaceLister
}

// layer1VirtualNetworksState is the first layer of state associated with all
// the relevant virtual networks.
// Its main purposes are retrieval of remote NetworkAttachments by workers and
// deletion of network interfaces of those remote NetworkAttachments when the
// virtual network becomes irrelevant.
// All operations on a layer1VirtualNetworksState must be done while holding
// its mutex's lock.
type layer1VirtualNetworksState struct {
	sync.RWMutex

	// attToSeenInCaches maps NetworkAttachments namespaced names to the list
	// of IDs of the Informer's caches where the attachments have been seen.
	attToSeenInCaches map[k8stypes.NamespacedName]map[cacheID]struct{}

	// vniToVNState maps a VNI to its layer1VirtualNetworkState.
	vniToVNState map[uint32]*layer1VirtualNetworkState
}

// layer2VirtualNetworkState is the second layer of state associated with a
// single virtual network.
type layer2VirtualNetworkState struct {
	// Kubernetes API namespace of the virtual network this
	// layer2VirtualNetworkState represents.
	namespace string

	// Names (namespace is the field above) of the local NetworkAttachments in
	// the virtual network. Used to detect when the virtual network becomes
	// irrelevant. It is populated by the workers processing the
	// NetworkAttachments in it.
	localAtts map[string]struct{}

	// Infomer on the remote NetworkAttachments in the virtual network.
	remoteAttsInformer k8scache.SharedIndexInformer

	// Channel to close to stop remoteAttsInformer when the virtual network
	// becomes irrelevant.
	remoteAttsInformerStopCh chan struct{}
}

// layer2VirtualNetworksState is the second layer of state associated with all
// the relevant virtual networks. Its main purposes are set up of layer1VNState
// when a virtual network becomes relevant and clearing such layer1VNState when
// the associated virtual network becomes irrelevant (and this in turn triggers
// deletion of the network interfaces of remote NetworkAttachments in that
// virtual network).
// All operations on a layer2VirtualNetworksState while queue workers are
// running must be done with the mutex locked.
type layer2VirtualNetworksState struct {
	sync.Mutex

	// localAttToLayer2VNI maps a local NetworkAttachment namespaced name to
	// the VNI of the layer2VirtualNetworkState where it's stored.
	localAttToLayer2VNI map[k8stypes.NamespacedName]uint32

	// vniToVNState maps a VNI to its layer2VirtualNetworkState.
	vniToVNState map[uint32]*layer2VirtualNetworkState

	// nextLayer1VNStateUID is the uid to assign to the next
	// layer1VirtualNetworkState that will be created.
	nextLayer1VNStateUID uint64
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
	node          string
	hostIP        gonet.IP
	kcs           *kosclientset.Clientset
	netv1a1Ifc    netvifc1a1.NetworkV1alpha1Interface
	eventRecorder k8seventrecord.EventRecorder
	queue         k8sworkqueue.RateLimitingInterface
	workers       int
	netFabric     netfabric.Interface
	stopCh        <-chan struct{}

	// Informer and lister on NetworkAttachments on the same node as the
	// connection agent.
	localAttsInformer k8scache.SharedIndexInformer
	localAttsLister   koslisterv1a1.NetworkAttachmentLister

	// Layer 1 of the state associated with all relevant virtual networks.
	// Always access while holding its mutex.
	// Never attempt to lock l2VirtNetsState's mutex while holding
	// l1VirtNetsState's, it can lead to deadlock.
	l1VirtNetsState *layer1VirtualNetworksState

	// Layer 2 of the state associated with all relevant virtual networks.
	// Always access while holding its mutex.
	// It is safe to attempt to lock l1VirtNetsState's mutex while holding
	// l2VirtNetsState's.
	l2VirtNetsState *layer2VirtualNetworksState

	// attToNetworkInterface maps NetworkAttachments namespaced names to their
	// network interfaces.
	// attToPostCreateExecReport maps local NetworkAttachments namespaced names
	// to the report of the command that was executed after creating their
	// network interface.
	// Access both only while holding attToNetworkInterfaceMutex.
	attToNetworkInterface      map[k8stypes.NamespacedName]networkInterface
	attToPostCreateExecReport  map[k8stypes.NamespacedName]*netv1a1.ExecReport
	attToNetworkInterfaceMutex sync.RWMutex

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
func New(node string,
	hostIP gonet.IP,
	kcs *kosclientset.Clientset,
	eventIfc k8scorev1client.EventInterface,
	queue k8sworkqueue.RateLimitingInterface,
	workers int,
	netFabric netfabric.Interface,
	allowedPrograms map[string]struct{}) *ConnectionAgent {

	attachmentCreateToLocalIfcHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace:   metricsNamespace,
			Subsystem:   metricsSubsystem,
			Name:        "attachment_create_to_local_ifc_latency_seconds",
			Help:        "Seconds from attachment CreationTimestamp to finished creating local interface",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": node},
		})
	attachmentCreateToRemoteIfcHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace:   metricsNamespace,
			Subsystem:   metricsSubsystem,
			Name:        "attachment_create_to_remote_ifc_latency_seconds",
			Help:        "Seconds from attachment CreationTimestamp to finished creating remote interface",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": node},
		})
	fabricLatencyHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace:   metricsNamespace,
			Subsystem:   metricsSubsystem,
			Name:        "fabric_latency_seconds",
			Help:        "Network fabric operation time in seconds",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16},
			ConstLabels: map[string]string{"node": node},
		},
		[]string{"op", "err"})
	attachmentCreateToStatusHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace:   metricsNamespace,
			Subsystem:   metricsSubsystem,
			Name:        "attachment_create_to_status_latency_seconds",
			Help:        "Seconds from attachment CreationTimestamp to return from successful status update",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": node},
		})
	attachmentStatusHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace:   metricsNamespace,
			Subsystem:   metricsSubsystem,
			Name:        "attachment_status_latency_seconds",
			Help:        "Round trip latency to update attachment status, in seconds",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": node},
		},
		[]string{"statusErr", "err"})
	localAttachmentsGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace:   metricsNamespace,
			Subsystem:   metricsSubsystem,
			Name:        "local_attachments",
			Help:        "Number of local attachments in network fabric",
			ConstLabels: map[string]string{"node": node},
		})
	remoteAttachmentsGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace:   metricsNamespace,
			Subsystem:   metricsSubsystem,
			Name:        "remote_attachments",
			Help:        "Number of remote attachments in network fabric",
			ConstLabels: map[string]string{"node": node},
		})
	attachmentExecDurationHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace:   metricsNamespace,
			Subsystem:   metricsSubsystem,
			Name:        "attachment_exec_duration_secs",
			Help:        "Time to run attachment commands, in seconds",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": node},
		},
		[]string{"what"})
	attachmentExecStatusCounts := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace:   metricsNamespace,
			Subsystem:   metricsSubsystem,
			Name:        "attachment_exec_status_count",
			Help:        "Counts of commands by what and exit status",
			ConstLabels: map[string]string{"node": node},
		},
		[]string{"what", "exitStatus"})
	fabricNameCounts := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace:   metricsNamespace,
			Subsystem:   metricsSubsystem,
			Name:        "fabric_count",
			Help:        "Indicator of chosen fabric implementation",
			ConstLabels: map[string]string{"node": node},
		},
		[]string{"fabric"})
	workerCount := prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace:   metricsNamespace,
			Subsystem:   metricsSubsystem,
			Name:        "worker_count",
			Help:        "Number of queue worker threads",
			ConstLabels: map[string]string{"node": node},
		})
	prometheus.MustRegister(attachmentCreateToLocalIfcHistogram, attachmentCreateToRemoteIfcHistogram, fabricLatencyHistograms, attachmentCreateToStatusHistogram, attachmentStatusHistograms, localAttachmentsGauge, remoteAttachmentsGauge, attachmentExecDurationHistograms, attachmentExecStatusCounts, fabricNameCounts, workerCount)

	fabricNameCounts.With(prometheus.Labels{"fabric": netFabric.Name()}).Inc()
	workerCount.Add(float64(workers))

	eventBroadcaster := k8seventrecord.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.V(3).Infof)
	eventBroadcaster.StartRecordingToSink(&k8scorev1client.EventSinkImpl{eventIfc})
	eventRecorder := eventBroadcaster.NewRecorder(kosscheme.Scheme, k8scorev1api.EventSource{Component: "connection-agent", Host: node})

	return &ConnectionAgent{
		node:          node,
		hostIP:        hostIP,
		kcs:           kcs,
		netv1a1Ifc:    kcs.NetworkV1alpha1(),
		eventRecorder: eventRecorder,
		queue:         queue,
		workers:       workers,
		netFabric:     netFabric,
		l1VirtNetsState: &layer1VirtualNetworksState{
			attToSeenInCaches: make(map[k8stypes.NamespacedName]map[cacheID]struct{}),
			vniToVNState:      make(map[uint32]*layer1VirtualNetworkState),
		},
		l2VirtNetsState: &layer2VirtualNetworksState{
			localAttToLayer2VNI: make(map[k8stypes.NamespacedName]uint32),
			vniToVNState:        make(map[uint32]*layer2VirtualNetworkState),
		},
		attToNetworkInterface:                make(map[k8stypes.NamespacedName]networkInterface),
		attToPostCreateExecReport:            make(map[k8stypes.NamespacedName]*netv1a1.ExecReport),
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

	ca.stopCh = stopCh

	ca.initLocalAttsInformerAndLister()
	go ca.localAttsInformer.Run(stopCh)
	klog.V(2).Infoln("Local NetworkAttachments informer started")

	if !k8scache.WaitForCacheSync(stopCh, ca.localAttsInformer.HasSynced) {
		return fmt.Errorf("Local NetworkAttachments informer failed to sync")
	}
	klog.V(2).Infoln("Local NetworkAttachments informer synced")

	if err := ca.syncPreExistingNetworkInterfaces(); err != nil {
		return err
	}
	klog.V(2).Infoln("Pre-existing network interfaces synced")

	// Serve Prometheus metrics.
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		klog.Errorf("In-process HTTP server crashed: %s", http.ListenAndServe(metricsAddr, nil).Error())
	}()

	for i := 0; i < ca.workers; i++ {
		go k8swait.Until(ca.processQueue, time.Second, stopCh)
	}
	klog.V(2).Infof("Launched %d workers", ca.workers)

	<-stopCh
	return nil
}

func (ca *ConnectionAgent) initLocalAttsInformerAndLister() {
	ca.localAttsInformer, ca.localAttsLister = ca.newInformerAndLister(resyncPeriod, k8smetav1.NamespaceAll, ca.localAttSelector(), attVNIAndIP)

	ca.localAttsInformer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
		AddFunc:    ca.onLocalAttAdd,
		UpdateFunc: ca.onLocalAttUpdate,
		DeleteFunc: ca.onLocalAttDelete})
}

func (ca *ConnectionAgent) onLocalAttAdd(obj interface{}) {
	att := obj.(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Local NetworkAttachments cache: notified of addition of %#+v", att)

	attNSN := parse.AttNSN(att)
	ca.updateL1VNStateForLocalAtt(attNSN, true)
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
	ca.updateL1VNStateForLocalAtt(attNSN, false)
	ca.queue.Add(attNSN)
}

func (ca *ConnectionAgent) updateL1VNStateForLocalAtt(att k8stypes.NamespacedName, attExists bool) {
	ca.l1VirtNetsState.Lock()
	defer ca.l1VirtNetsState.Unlock()

	attSeenInCaches := ca.l1VirtNetsState.attToSeenInCaches[att]

	if attExists {
		if attSeenInCaches == nil {
			attSeenInCaches = make(map[cacheID]struct{}, 1)
			ca.l1VirtNetsState.attToSeenInCaches[att] = attSeenInCaches
		}
		attSeenInCaches[localAttsCacheID] = struct{}{}
		return
	}

	delete(attSeenInCaches, localAttsCacheID)
	if len(attSeenInCaches) == 0 {
		delete(ca.l1VirtNetsState.attToSeenInCaches, att)
	}
}

func (ca *ConnectionAgent) syncPreExistingNetworkInterfaces() error {
	// Start all the remote attachments informers because to choose whether to
	// keep a pre-existing remote network interface we need to look for a
	// remote network attachment that can own it in the informer cache for the
	// VNI of the network interface.
	err := ca.startRemoteAttsInformers()
	if err != nil {
		return fmt.Errorf("failed to sync pre-existing network interfaces: %s", err.Error())
	}

	ifcs, err := ca.listPreExistingNetworkInterfaces()
	if err != nil {
		return fmt.Errorf("failed to sync pre-existing network interfaces: %s", err.Error())
	}

	for _, ifc := range ifcs {
		err = ca.syncPreExistingNetworkInterface(ifc)
		if err != nil {
			return err
		}
	}

	return nil
}

func (ca *ConnectionAgent) startRemoteAttsInformers() error {
	localAtts, err := ca.localAttsLister.List(k8slabels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list local network attachments: %s", err.Error())
	}

	for _, att := range localAtts {
		// Adding the local NetworkAttachment to the L2 virtual network state
		// entails starting the remote attachments informer for the attachment's
		// vni.
		ca.addLocalAttToL2VNState(parse.AttNSN(att), att.Status.AddressVNI)
		l2VNState := ca.l2VirtNetsState.vniToVNState[att.Status.AddressVNI]
		if !l2VNState.remoteAttsInformer.HasSynced() && !k8scache.WaitForCacheSync(l2VNState.remoteAttsInformerStopCh, l2VNState.remoteAttsInformer.HasSynced) {
			return fmt.Errorf("failed to sync remote attachments informer for VNI %#x", att.Status.AddressVNI)
		}
	}

	return nil
}

func (ca *ConnectionAgent) syncPreExistingNetworkInterface(ifc networkInterface) error {
	// Retrieve the indexer where an attachment elegible to own the network
	// interface is, assuming one exists.
	indexer, err := ca.getIndexerForNetworkInterface(ifc)
	if err != nil {
		return fmt.Errorf("failed to retrieve indexer for network interface %s", ifc)
	}

	// Retrieve the attachment elegible to own the network interface, assuming
	// one exists.
	var ifcOwner *netv1a1.NetworkAttachment
	if indexer != nil {
		ifcOwnerObj, err := indexer.ByIndex(ifcOwnerDataIndexerName, ifc.index())
		if err != nil {
			return fmt.Errorf("ByIndex(%s, %s) failed: %s", ifcOwnerDataIndexerName, ifc.index(), err.Error())
		}
		if len(ifcOwnerObj) == 1 {
			ifcOwner, _ = ifcOwnerObj[0].(*netv1a1.NetworkAttachment)
		}
	}

	if ifcOwner != nil {
		ifcOwnerNSN := parse.AttNSN(ifcOwner)
		_, ownerAlreadyHasInterface := ca.getNetworkInterface(ifcOwnerNSN)

		// Pre-existing Network interfaces are matched to network attachments
		// on the basis of the VNI, the IP and the host of the attachment. But
		// these fields can be seen changing even if the namespaced name is
		// steady, for instance by deleting the network attachment and creating
		// one with same namespaced name and a different VNI. This means that
		// two network interfaces could be matched to the same namespaced name,
		// where each match would correspond to two different versions of the
		// same network attachment, or two different network attachments with
		// the same namespaced name. In such cases, the interface to keep is the
		// one matching the most recent version of the network attachment, while
		// the other should be deleted. Notice that the order in which the
		// matches took place does not say which match should be kept, because
		// the two versions of the matched network attachment might come from
		// different informers and there are no cross-informer ordering
		// guarantees. There are ways to always take the optimal choice, but
		// they make for really complex code and yield little advantage because
		// attachments can change: if an attachment is matched and it changes 1
		// sec later, the match was useless. For this reason, this code does not
		// attempt to always take the optimal choice: in cases of collisions the
		// interface that was matched first is taken and the other is deleted.
		// Collisions should be a rare event anyway. Even if the wrong choice is
		// taken during normal operation the connection agent will rectify the
		// the mistake by realizing the network interface does not match the
		// attachment and creating a correct interface after deleting the old one.
		if !ownerAlreadyHasInterface {
			ca.assignNetworkInterface(ifcOwnerNSN, ifc)
			klog.V(3).Infof("Matched pre-existing network interface %s with attachment %s", ifc, ifcOwnerNSN)
			if localIfc, ifcIsLocal := ifc.(*localNetworkInterface); ifcIsLocal {
				ca.setExecReport(ifcOwnerNSN, localIfc.id, ifcOwner.Status.PostCreateExecReport)
				localIfc.postDeleteExec = ifcOwner.Spec.PostDeleteExec
			} else {
				// The interface is remote and so is its owner. There's no
				// guarantee that the add notification handler for the owner has
				// executed yet, neither as for when it will execute. If that
				// has not happened by the time a worker processes the deletion
				// of the last local NetworkAttachment in the owner's virtual
				// network, the layer 1 of the virtual network state is cleared
				// before the owner's namespaced name could be recorded in it.
				// This means the owner is not processed, hence its network
				// interface is not deleted even if it should. To avoid this,
				// enqueue the owner to force its processing.
				ca.queue.Add(ifcOwnerNSN)
			}
			return nil
		}
	}

	// No attachment elegible to own the network interface was found: delete it.
	ca.deleteOrphanNetworkInterface(ifc)
	return nil
}

func (ca *ConnectionAgent) deleteOrphanNetworkInterface(ifc networkInterface) {
	for i := 1; ; i++ {
		err := ca.deleteNetworkInterface(ifc)
		if err == nil {
			klog.V(4).Infof("Deleted pre-existing orphan network interface %s (attempt nbr. %d)", ifc, i)
			break
		}
		klog.Errorf("failed to delete pre-existing orphan network interface %s (attempt nbr. %d)", ifc, i)
	}
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

	err := ca.syncL2VNState(attNSN, att)
	if err != nil {
		return err
	}
	klog.V(3).Infof("Synced Layer2VNState for attachment %s.", attNSN)

	// Create/update/delete the network interface of the NetworkAttachment.
	localIfc, statusErrs, postCreateExecReport, err := ca.syncNetworkInterface(attNSN, att)
	if err != nil {
		return err
	}
	klog.V(3).Infof("Synced network interface for attachment %s", attNSN)

	// The only thing left to do is updating the NetworkAttachment status. If
	// it's not needed, return.
	if att == nil || ca.node != att.Spec.Node {
		return nil
	}
	// If we're here there's no doubt that the NetworkAttachment and its
	// network interface are local.
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
	// Get the lister backed by the Informer's cache where the NetworkAttachment
	// was seen. There could more than one.
	attSeenInCache, seenInMoreThanOneCache := ca.getSeenInCache(attNSN)

	if seenInMoreThanOneCache {
		// If the NetworkAttachment was seen in more than one informer's cache
		// the most up-to-date version is unkown. Halt processing until future
		// delete notifications from the informers storing stale versions arrive
		// and reveal the current state of the NetworkAttachment.
		klog.V(4).Infof("Cannot process NetworkAttachment %s because it was seen in more than one informer.", attNSN)
		haltProcessing = true
		return
	}

	if attSeenInCache == nil {
		// The NetworkAttachment was seen in no informer's cache: it must have
		// been deleted.
		return
	}

	// Retrieve the NetworkAttachment.
	// ? What happens if this .Get hits the cache after the associated Informer
	// has been stopped? I suspect nothing worth special care, but double-check
	// to make sure.
	att, err := attSeenInCache.Get(attNSN.Name)
	if err != nil && !k8serrors.IsNotFound(err) {
		klog.Errorf("Failed to look up NetworkAttachment %s: %s. This should never happen, there will be no retry.", attNSN, err.Error())
		haltProcessing = true
	}

	return
}

func (ca *ConnectionAgent) getSeenInCache(att k8stypes.NamespacedName) (seenInCache koslisterv1a1.NetworkAttachmentNamespaceLister, seenInMoreThanOneCache bool) {
	ca.l1VirtNetsState.RLock()
	defer ca.l1VirtNetsState.RUnlock()

	seenInCachesIDs := ca.l1VirtNetsState.attToSeenInCaches[att]

	if len(seenInCachesIDs) > 1 {
		seenInMoreThanOneCache = true
		return
	}

	if len(seenInCachesIDs) == 0 {
		return
	}

	var seenInCacheID cacheID
	for seenInCacheID = range seenInCachesIDs {
	}

	if seenInCacheID == localAttsCacheID {
		seenInCache = ca.localAttsLister.NetworkAttachments(att.Namespace)
	} else {
		attLayer1VNState := ca.l1VirtNetsState.vniToVNState[uint32(seenInCacheID)]
		seenInCache = attLayer1VNState.remoteAttsLister
	}
	return
}

func (ca *ConnectionAgent) syncL2VNState(attNSN k8stypes.NamespacedName, att *netv1a1.NetworkAttachment) error {
	ca.l2VirtNetsState.Lock()
	defer ca.l2VirtNetsState.Unlock()

	attOldLayer2VNI, attOldLayer2VNIFound := ca.l2VirtNetsState.localAttToLayer2VNI[attNSN]
	if attOldLayer2VNIFound && (att == nil || attOldLayer2VNI != att.Status.AddressVNI || ca.node != att.Spec.Node) {
		// The NetworkAttachment was local and recorded in a
		// layer2VirtualNetworkState, but now it should no longer be there
		// because its state has changed: remove it.
		ca.removeLocalAttFromL2VNState(attNSN, attOldLayer2VNI)
	}

	if att != nil && ca.node == att.Spec.Node && (!attOldLayer2VNIFound || attOldLayer2VNI != att.Status.AddressVNI) {
		// The NetworkAttachment is local and is not in the
		// layer2VirtualNetworkState of its virtual network yet: add it.
		return ca.addLocalAttToL2VNState(attNSN, att.Status.AddressVNI)
	}

	return nil
}

// addLocalAttToLayer2VNState adds a local NetworkAttachment to its
// layer2VirtualNetworkState and inits such state if the NetworkAttachment is
// the first local one (this entails initializing the layer1VirtualNetworkState
// as well).
func (ca *ConnectionAgent) addLocalAttToL2VNState(att k8stypes.NamespacedName, vni uint32) error {
	attL2VNState := ca.l2VirtNetsState.vniToVNState[vni]
	if attL2VNState == nil {
		// The NetworkAttachment is the first local one for its virtual network,
		// which has therefore just become relevant.
		attL2VNState = ca.initLayer2VNState(vni, att.Namespace)
		klog.V(2).Infof("Virtual Network with VNI %d became relevant because of creation of first local attachment %s. Its state has been initialized.", vni, att)
	}

	if attL2VNState.namespace != att.Namespace {
		// If the NetworkAttachment's namespace does not match the one of the
		// layer2VirtNetState for its vni X a virtual network with vni X must
		// have been deleted (AKA all its subnets have been) right before a new
		// one with the same vni but different namespace was created, but the
		// connection agent has not processed all the notifications yet.
		// Return an error to trigger delayed reprocessing, when (hopefully)
		// all the notifications have been processed.
		return fmt.Errorf("attachment is local but could not be added to layer2VirtualNetworkState because namespace found there (%s) does not match the attachment's", attL2VNState.namespace)
	}

	ca.l2VirtNetsState.localAttToLayer2VNI[att] = vni
	attL2VNState.localAtts[att.Name] = struct{}{}
	return nil
}

// removeLocalAttFromLayer2VNState removes a local NetworkAttachment from its
// layer2VirtualNetworkState and clears such state if the NetworkAttachment was
// the last local one (this entails clearing the layer1VirtualNetworkState as well).
// Invoke only while holding ca.l2VirtNetsState's mutex.
func (ca *ConnectionAgent) removeLocalAttFromL2VNState(att k8stypes.NamespacedName, vni uint32) {
	oldLayer2VNState := ca.l2VirtNetsState.vniToVNState[vni]
	delete(oldLayer2VNState.localAtts, att.Name)
	delete(ca.l2VirtNetsState.localAttToLayer2VNI, att)

	if len(oldLayer2VNState.localAtts) == 0 {
		// Clear all resources associated with the virtual network because the
		// last local NetworkAttachment in it has been deleted and it has thus
		// become irrelevant.
		delete(ca.l2VirtNetsState.vniToVNState, vni)
		close(oldLayer2VNState.remoteAttsInformerStopCh)
		ca.clearLayer1VNState(vni, oldLayer2VNState.namespace)
		klog.V(2).Infof("Virtual Network with VNI %d became irrelevant because of deletion of last local attachment %s. Its state has been cleared.", vni, att)
	}
}

// initLayer2VNState configures and starts the Informer for remote
// NetworkAttachments in the virtual network identified by `vni`.
// It also initializes the layer1VirtualNetworkState corresponding to `vni`.
func (ca *ConnectionAgent) initLayer2VNState(vni uint32, namespace string) *layer2VirtualNetworkState {
	remAttsInformer, remAttsLister := ca.newInformerAndLister(resyncPeriod, namespace, ca.remoteAttSelector(vni), attHostIPAndIP)
	newLayer2VNState := &layer2VirtualNetworkState{
		namespace:                namespace,
		localAtts:                make(map[string]struct{}, 1),
		remoteAttsInformer:       remAttsInformer,
		remoteAttsInformerStopCh: make(chan struct{}),
	}
	ca.l2VirtNetsState.vniToVNState[vni] = newLayer2VNState

	l1VNStateUID := ca.l2VirtNetsState.nextLayer1VNStateUID
	ca.l2VirtNetsState.nextLayer1VNStateUID++

	ca.initLayer1VNState(vni, l1VNStateUID, remAttsLister.NetworkAttachments(namespace))

	remAttsInformer.AddEventHandler(ca.newRemoteAttsEventHandler(l1VNStateUID))
	go remAttsInformer.Run(mergeStopChannels(ca.stopCh, newLayer2VNState.remoteAttsInformerStopCh))

	return newLayer2VNState
}

func (ca *ConnectionAgent) initLayer1VNState(vni uint32, uid uint64, remAttsLister koslisterv1a1.NetworkAttachmentNamespaceLister) {
	ca.l1VirtNetsState.Lock()
	defer ca.l1VirtNetsState.Unlock()

	ca.l1VirtNetsState.vniToVNState[vni] = &layer1VirtualNetworkState{
		uid:              uid,
		remoteAtts:       make(map[string]struct{}),
		remoteAttsLister: remAttsLister}
}

func (ca *ConnectionAgent) clearLayer1VNState(vni uint32, namespace string) {
	ca.l1VirtNetsState.Lock()
	defer ca.l1VirtNetsState.Unlock()

	layer1VNState := ca.l1VirtNetsState.vniToVNState[vni]
	delete(ca.l1VirtNetsState.vniToVNState, vni)
	for aRemoteAtt := range layer1VNState.remoteAtts {
		aRemoteAttNSN := k8stypes.NamespacedName{Namespace: namespace,
			Name: aRemoteAtt}
		delete(ca.l1VirtNetsState.attToSeenInCaches[aRemoteAttNSN], cacheID(vni))
		ca.queue.Add(aRemoteAttNSN)
	}
}

func (ca *ConnectionAgent) newRemoteAttsEventHandler(vnStateUID uint64) k8scache.ResourceEventHandlerFuncs {
	onRemoteAttAdd := func(obj interface{}) {
		att := obj.(*netv1a1.NetworkAttachment)
		klog.V(5).Infof("Remote NetworkAttachments cache for VNI %06x: notified of addition of %#+v", att.Status.AddressVNI, att)

		attNSN := parse.AttNSN(att)
		added := ca.updateL1VNStateForRemoteAtt(attNSN, att.Status.AddressVNI, vnStateUID, true)
		if added {
			ca.queue.Add(attNSN)
		}
	}

	onRemoteAttUpdate := func(oldObj, obj interface{}) {
		oldAtt, att := oldObj.(*netv1a1.NetworkAttachment), obj.(*netv1a1.NetworkAttachment)
		klog.V(5).Infof("Remote NetworkAttachments cache for VNI %06x: notified of update from %#+v to %#+v.", att.Status.AddressVNI, oldAtt, att)

		// The only fields affecting remote network interfaces handling that can
		// be seen changing by this function are status.ipv4 and status.hostIP,
		// so enqueue only if they changed.
		if oldAtt.Status.IPv4 != att.Status.IPv4 || oldAtt.Status.HostIP != att.Status.HostIP {
			ca.queue.Add(parse.AttNSN(att))
		}
	}

	onRemoteAttDelete := func(obj interface{}) {
		att := parse.Peel(obj).(*netv1a1.NetworkAttachment)
		klog.V(5).Infof("Remote NetworkAttachments cache for VNI %06x: notified of deletion of %#+v", att.Status.AddressVNI, att)

		attNSN := parse.AttNSN(att)
		removed := ca.updateL1VNStateForRemoteAtt(attNSN, att.Status.AddressVNI, vnStateUID, false)
		if removed {
			ca.queue.Add(attNSN)
		}
	}

	return k8scache.ResourceEventHandlerFuncs{
		AddFunc:    onRemoteAttAdd,
		UpdateFunc: onRemoteAttUpdate,
		DeleteFunc: onRemoteAttDelete,
	}
}

func (ca *ConnectionAgent) updateL1VNStateForRemoteAtt(att k8stypes.NamespacedName, vni uint32, vnStateUID uint64, attExists bool) (updated bool) {
	ca.l1VirtNetsState.Lock()
	defer ca.l1VirtNetsState.Unlock()

	attLayer1VNState := ca.l1VirtNetsState.vniToVNState[vni]

	// The check for non-nilness handles cases where the virtual network has
	// become irrelevant and its state has been cleared after this function
	// started executing but before it could acquire the lock on ca.l1VirtNetsState.
	// The check on UIDs handles the cases where the virtual network became
	// irrelevant and its state was cleared and then it became relevant again
	// and its state was re-initialized, all between start of execution of this
	// function and acquisition of ca.l1VirtNetsState.
	if attLayer1VNState == nil || attLayer1VNState.uid != vnStateUID {
		return
	}

	attSeenInCaches := ca.l1VirtNetsState.attToSeenInCaches[att]
	if attExists {
		if attSeenInCaches == nil {
			attSeenInCaches = make(map[cacheID]struct{}, 1)
			ca.l1VirtNetsState.attToSeenInCaches[att] = attSeenInCaches
		}
		attSeenInCaches[cacheID(vni)] = struct{}{}
		attLayer1VNState.remoteAtts[att.Name] = struct{}{}
	} else {
		delete(attSeenInCaches, cacheID(vni))
		if len(attSeenInCaches) == 0 {
			delete(ca.l1VirtNetsState.attToSeenInCaches, att)
		}
		delete(attLayer1VNState.remoteAtts, att.Name)
	}

	updated = true
	return
}

func (ca *ConnectionAgent) syncNetworkInterface(attNSN k8stypes.NamespacedName, att *netv1a1.NetworkAttachment) (localIfc *localNetworkInterface, statusErrs sliceOfString, postCreateER *netv1a1.ExecReport, err error) {
	oldIfc, oldIfcFound := ca.getNetworkInterface(attNSN)
	oldIfcCanBeUsed := oldIfcFound && oldIfc.canBeOwnedBy(att)

	if oldIfcFound && !oldIfcCanBeUsed {
		err = ca.deleteNetworkInterface(oldIfc)
		if err != nil {
			return
		}
		ca.unassignNetworkInterface(attNSN)
		klog.V(4).Infof("Deleted network interface %s for attachment %s", oldIfc, attNSN)
		if oldLocalIfc, oldIfcIsLocal := oldIfc.(*localNetworkInterface); oldIfcIsLocal {
			ca.unsetExecReport(attNSN)
			ca.launchCommand(attNSN, oldLocalIfc.LocalNetIfc, oldLocalIfc.id, oldLocalIfc.postDeleteExec, "postDelete", true, false)
		}
	}

	if att == nil {
		return
	}

	if oldIfcCanBeUsed {
		if oldLocalIfc, oldIfcIsLocal := oldIfc.(*localNetworkInterface); oldIfcIsLocal {
			statusErrs = ca.launchCommand(attNSN, oldLocalIfc.LocalNetIfc, oldLocalIfc.id, att.Spec.PostCreateExec, "postCreate", false, false)
			postCreateER = ca.getExecReport(attNSN)
			localIfc = oldLocalIfc
		}
		klog.V(4).Infof("Attachment %s can use old network interface %s.", attNSN, oldIfc)
		return
	}

	ifc, err := ca.createNetworkInterface(att)
	if err != nil {
		return
	}
	ca.assignNetworkInterface(attNSN, ifc)
	klog.V(4).Infof("Created network interface %s for attachment %s", ifc, attNSN)
	localIfc, ifcIsLocal := ifc.(*localNetworkInterface)
	if ifcIsLocal {
		ca.eventRecorder.Eventf(att, k8scorev1api.EventTypeNormal, "Implemented", "Created Linux network interface named %s with MAC address %s and IPv4 address %s", localIfc.Name, localIfc.GuestMAC, localIfc.GuestIP)
		statusErrs = ca.launchCommand(attNSN, localIfc.LocalNetIfc, localIfc.id, att.Spec.PostCreateExec, "postCreate", true, true)
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

func (ca *ConnectionAgent) getNetworkInterface(att k8stypes.NamespacedName) (ifc networkInterface, ifcFound bool) {
	ca.attToNetworkInterfaceMutex.RLock()
	defer ca.attToNetworkInterfaceMutex.RUnlock()

	ifc, ifcFound = ca.attToNetworkInterface[att]
	return
}

func (ca *ConnectionAgent) assignNetworkInterface(att k8stypes.NamespacedName, ifc networkInterface) {
	ca.attToNetworkInterfaceMutex.Lock()
	defer ca.attToNetworkInterfaceMutex.Unlock()

	ca.attToNetworkInterface[att] = ifc
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

func (ca *ConnectionAgent) unsetExecReport(att k8stypes.NamespacedName) {
	ca.attToNetworkInterfaceMutex.Lock()
	defer ca.attToNetworkInterfaceMutex.Unlock()

	delete(ca.attToPostCreateExecReport, att)
}

func (ca *ConnectionAgent) getExecReport(att k8stypes.NamespacedName) *netv1a1.ExecReport {
	ca.attToNetworkInterfaceMutex.RLock()
	defer ca.attToNetworkInterfaceMutex.RUnlock()

	return ca.attToPostCreateExecReport[att]
}

func (ca *ConnectionAgent) unassignNetworkInterface(att k8stypes.NamespacedName) {
	ca.attToNetworkInterfaceMutex.Lock()
	defer ca.attToNetworkInterfaceMutex.Unlock()

	delete(ca.attToNetworkInterface, att)
}

// localAttSelector returns a fields selector that matches local
// NetworkAttachments for whom a network interface can be created.
func (ca *ConnectionAgent) localAttSelector() fieldsSelector {
	// The NetworkAttachment must be local.
	localAtt := k8sfields.OneTermEqualSelector(attNodeField, ca.node)

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
	remoteAtt := k8sfields.OneTermNotEqualSelector(attNodeField, ca.node)

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

func (ca *ConnectionAgent) newInformerAndLister(resyncPeriod time.Duration, ns string, fs fieldsSelector, indexer k8scache.IndexFunc) (k8scache.SharedIndexInformer, koslisterv1a1.NetworkAttachmentLister) {
	tloFunc := fs.toTweakListOptionsFunc()
	networkAttachments := kosinformers.NewSharedInformerFactoryWithOptions(ca.kcs, resyncPeriod, kosinformers.WithNamespace(ns), kosinformers.WithTweakListOptions(tloFunc)).Network().V1alpha1().NetworkAttachments()

	// Add indexer used at start up to match pre-existing network interfaces to
	// owning NetworkAttachment (if one exists).
	networkAttachments.Informer().AddIndexers(map[string]k8scache.IndexFunc{ifcOwnerDataIndexerName: indexer})

	return networkAttachments.Informer(), networkAttachments.Lister()
}

// getRemoteAttsIndexer returns the Indexer associated with the remote
// NetworkAttachments informer for `vni`.
// If there's no such indexer because `vni` is irrelevant, nil is returned.
// It is used only while syncing pre-existing network interfaces, when there's
// only one goroutine, so it accesses layer 2 virtual network state without
// holding the appropriate mutex.
func (ca *ConnectionAgent) getRemoteAttsIndexer(vni uint32) k8scache.Indexer {
	l2VNState := ca.l2VirtNetsState.vniToVNState[vni]
	if l2VNState != nil {
		return l2VNState.remoteAttsInformer.GetIndexer()
	}
	return nil
}
