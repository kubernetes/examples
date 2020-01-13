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
	netifcv1a1 "k8s.io/examples/staging/kos/pkg/client/clientset/versioned/typed/network/v1alpha1"
	kosinformers "k8s.io/examples/staging/kos/pkg/client/informers/externalversions"
	koslisterv1a1 "k8s.io/examples/staging/kos/pkg/client/listers/network/v1alpha1"
	netfabric "k8s.io/examples/staging/kos/pkg/networkfabric"
	"k8s.io/examples/staging/kos/pkg/util/parse"
)

const (
	// localAttsInformerFakeVNI is the fake VNI used to identify the informer on
	// local NetworkAttachments. Remote NetworkAttachments informers are
	// partitioned by VNI so they can be identified via their VNI, but the local
	// NetworkAttachments informer is not associated to a single VNI. Pick 0
	// because it's not a valid VNI: there's no overlapping with remote
	// NetworkAttachments informers' VNIs.
	localAttsInformerFakeVNI = 0

	// Name of the indexer used to match pre-existing network interfaces to
	// network attachments.
	// In the informer for local NetworkAttachments the indexed values are <VNI>/<GuestIP>.
	// In an informer for remote NetworkAttachments the indexed values are <HostIP>/<GuestIP>.
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

// stage1VirtualNetworkState is the first stage of the state associated with a
// single relevant virtual network.
type stage1VirtualNetworkState struct {
	// List of names of remote NetworkAttachments in the virtual network (which
	// implicitly determines the namespace) for whom an add notification handler
	// has been executed but a delete notification handler has not.
	remoteAtts map[string]struct{}

	// Lister used by workers to retrieve the NetworkAttachment they're
	// processing.
	remoteAttsLister koslisterv1a1.NetworkAttachmentNamespaceLister
}

// stage1VirtualNetworksState is the first stage of the state associated with
// all the relevant virtual networks.
// Its main purposes are retrieval of remote NetworkAttachments by workers and
// deletion of network interfaces of those remote NetworkAttachments when the
// virtual network becomes irrelevant.
// All operations on a stage1VirtualNetworksState must be done while holding
// its mutex's lock.
type stage1VirtualNetworksState struct {
	sync.RWMutex

	// For a namespaced name X, attToVNIs[X] stores the list of VNIs of the
	// informers whose cache stores* a NetworkAttachment with namespaced name X.
	// A VNI Y is added to attToVNIs[X] when a create notification for a
	// NetworkAttachment with namespaced name X is received by the informer
	// associated to VNI Y and is removed from attToVNIs[X] when a delete
	// notification for a NetworkAttachment with namespaced name X is received
	// by the informer associated to VNI Y. Notice that the local
	// NetworkAttachments informer is not associated to a single VNI. To
	// represent it in attToVNIs, 0 is used as a fake VNI, as it's not a valid
	// VNI: there's no risk of collisions with the VNIs of remote attachments'
	// informers (which are characterized by a 1-to-1 relationship with VNIs).
	//
	// * The actual addition/deletion of VNIs to/from attToVNIs is done by
	// informers' notification handlers, which execute after the corresponding
	// cache modification. This means that attToVNIs lags behind the actual
	// content of the informers' caches.
	attToVNIs map[k8stypes.NamespacedName]map[uint32]struct{}

	// vniToVNState maps a VNI to its stage1VirtualNetworkState.
	vniToVNState map[uint32]*stage1VirtualNetworkState
}

// stage2VirtualNetworkState is the second stage of the state associated with a
// single virtual network.
type stage2VirtualNetworkState struct {
	// Kubernetes API namespace of the virtual network this
	// stage2VirtualNetworkState represents.
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

// stage2VirtualNetworksState is the second stage of the state associated with
// all the relevant virtual networks. Its main purposes are set up of
// stage1VNState when a virtual network becomes relevant and clearing such
// stage1VNState when the associated virtual network becomes irrelevant (and
// this in turn triggers deletion of the network interfaces of remote
// NetworkAttachments in that virtual network).
// All operations on a stage2VirtualNetworksState while queue workers are
// running must be done with the mutex locked.
type stage2VirtualNetworksState struct {
	sync.Mutex

	// localAttToStage2VNI maps a local NetworkAttachment namespaced name to
	// the VNI of the stage2VirtualNetworkState where it's stored.
	localAttToStage2VNI map[k8stypes.NamespacedName]uint32

	// vniToVNState maps a VNI to its stage2VirtualNetworkState.
	vniToVNState map[uint32]*stage2VirtualNetworkState
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
	netv1a1Ifc    netifcv1a1.NetworkV1alpha1Interface
	eventRecorder k8seventrecord.EventRecorder
	queue         k8sworkqueue.RateLimitingInterface
	workers       int
	netFabric     netfabric.Interface
	stopCh        <-chan struct{}

	// Informer and lister on NetworkAttachments on the same node as the
	// connection agent.
	localAttsInformer k8scache.SharedIndexInformer
	localAttsLister   koslisterv1a1.NetworkAttachmentLister

	// First stage of the state associated with all relevant virtual networks.
	// Always access while holding its mutex.
	// Never attempt to lock s2VirtNetsState's mutex while holding
	// s1VirtNetsState's, it can lead to deadlock.
	s1VirtNetsState stage1VirtualNetworksState

	// Second stage of the state associated with all relevant virtual networks.
	// Always access while holding its mutex.
	// It is safe to attempt to lock s1VirtNetsState's mutex while holding
	// s2VirtNetsState's.
	s2VirtNetsState stage2VirtualNetworksState

	// attToNetworkInterface maps NetworkAttachments namespaced names to their
	// network interfaces.
	// Access only while holding attToNetworkInterfaceMutex.
	attToNetworkInterface      map[k8stypes.NamespacedName]networkInterface
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
		s1VirtNetsState: stage1VirtualNetworksState{
			attToVNIs:    make(map[k8stypes.NamespacedName]map[uint32]struct{}),
			vniToVNState: make(map[uint32]*stage1VirtualNetworkState),
		},
		s2VirtNetsState: stage2VirtualNetworksState{
			localAttToStage2VNI: make(map[k8stypes.NamespacedName]uint32),
			vniToVNState:        make(map[uint32]*stage2VirtualNetworkState),
		},
		attToNetworkInterface:                make(map[k8stypes.NamespacedName]networkInterface),
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
	klog.V(5).Infof("Local NetworkAttachments informer: notified of addition of %#+v", att)

	attNSN := parse.AttNSN(att)
	ca.updateS1VNState(attNSN, localAttsInformerFakeVNI, nil, true)
	ca.queue.Add(attNSN)
}

func (ca *ConnectionAgent) onLocalAttUpdate(oldObj, obj interface{}) {
	oldAtt, att := oldObj.(*netv1a1.NetworkAttachment), obj.(*netv1a1.NetworkAttachment)

	// Enqueue if the UID changed because if a local NetworkAttachment is
	// deleted and replaced the status.hostIP field of the newer attachment is
	// set to "", and the connection agent has to write back the correct value.
	// Also, the only fields affecting local network interfaces handling that
	// can be seen changing by this function are status.ipv4 and
	// status.addressVNI, so enqueue if they changed.
	if oldAtt.UID != att.UID || oldAtt.Status.IPv4 != att.Status.IPv4 || oldAtt.Status.AddressVNI != att.Status.AddressVNI {
		klog.V(5).Infof("Local NetworkAttachments informer: notified of update from %#+v to %#+v. Relevant state changed, the attachment will be re-processed.", oldAtt, att)
		ca.queue.Add(parse.AttNSN(att))
	} else {
		klog.V(5).Infof("Local NetworkAttachments informer: notified of update from %#+v to %#+v. The update will be ignored because nothing relevant changed.", oldAtt, att)
	}
}

func (ca *ConnectionAgent) onLocalAttDelete(obj interface{}) {
	att := parse.Peel(obj).(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Local NetworkAttachments informer: notified of removal of %#+v", att)

	attNSN := parse.AttNSN(att)
	ca.updateS1VNState(attNSN, localAttsInformerFakeVNI, nil, false)
	ca.queue.Add(attNSN)
}

func (ca *ConnectionAgent) syncPreExistingNetworkInterfaces() error {
	// Start all the remote attachments informers because to choose whether to
	// keep a pre-existing remote network interface we need to look for a remote
	// network attachment that can own it in the informer cache for the VNI of
	// the network interface.
	err := ca.startRemoteAttsInformers()
	if err != nil {
		return fmt.Errorf("failed to start remote attachment informers during sync of pre-existing network interfaces: %s", err.Error())
	}

	ifcs, err := ca.listPreExistingNetworkInterfaces()
	if err != nil {
		return fmt.Errorf("failed to list pre-existing network interfaces: %s", err.Error())
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
		// Adding `att` to the S2 virtual network state entails starting the
		// remote attachments informer for `att`'s vni. addLocalAttToS2VNState
		// returns an error (and does not perform the addition) if `att`'s
		// namespace differs from the one recorded in the S2VNState for `att`'s
		// vni. The namespace recorded in such S2VNState is the namespace of the
		// first attachment added to it. If an error is returned, we're during a
		// transient where two or more NetworkAttachments with same VNI but
		// different namespace exist, caused by a virtual network being deleted
		// and replaced with same VNI but different namespace. Attachments with
		// the most recent namespace should be added to the S2VNState while the
		// others should be not added or removed if they are already there, but
		// it's unkown which namespace is the most recent one, `att`'s or the
		// one stored in the S2VNState. Thus, if an error is returned it is
		// ignored and `att` is not added to the S2VNState: the namespace
		// already recorded in the S2VNState is chosen for simplicity. Even if
		// that turns out to be the wrong choice, after the workers are started
		// delete notifications for all the attachments in the S2VNState will
		// arrive causing its deletion and the attachments which were not added
		// (such as `att`) will be processed and added to a new S2VNState with
		// the most recent namespace.
		ca.addLocalAttToS2VNState(parse.AttNSN(att), att.Status.AddressVNI)
		s2VNState := ca.s2VirtNetsState.vniToVNState[att.Status.AddressVNI]
		if !s2VNState.remoteAttsInformer.HasSynced() && !k8scache.WaitForCacheSync(s2VNState.remoteAttsInformerStopCh, s2VNState.remoteAttsInformer.HasSynced) {
			return fmt.Errorf("failed to sync remote attachments informer for VNI %#x", att.Status.AddressVNI)
		}
	}

	return nil
}

func (ca *ConnectionAgent) syncPreExistingNetworkInterface(ifc networkInterface) error {
	ifcOwner, err := ifc.findOwner(ca)
	if err != nil {
		return fmt.Errorf("failed to sync pre-existing network interface %s: %s", ifc, err.Error())
	}

	if ifcOwner != nil {
		ifcOwnerNSN := parse.AttNSN(ifcOwner)
		_, ownerAlreadyHasInterface := ca.getNetworkInterface(ifcOwnerNSN)

		// Pre-existing network interfaces are linked to their owners. The owner
		// of an interface is a network attachment that matches the interface
		// VNI, guest IP and host. If an interface I is linked to an attachment
		// with namespaced name A, the association is stored by the connection
		// agent as the pair (I, A), that is, the association keeps only the
		// namespaced name of the attachment. The attachment fields that are
		// used to link attachments and interfaces can be updated, and
		// attachments can be deleted and re-created with the same namespaced
		// name but different field values. This means that two interfaces could
		// be linked to the same namespaced name, where each link would be
		// associated to two different versions of the same attachment, or two
		// attachments with the same namespaced name (for the remainder of this
		// explanation only the first case will be considered, but the arguments
		// that are made apply to the second argument as well). In such case,
		// the interface to keep is the one associated to the most recent
		// version of the attachment, while the other interface should be
		// deleted. Note that the order in which the two associations were made
		// does not necessarily reflect the order of the versions of the
		// attachment: the out-of-date version might be the one in the
		// association that was made last. The reason is that the two versions
		// of the attachment might come from two different informers, and there
		// are no cross-informer ordering guarantees. There are ways to always
		// take the optimal choice, but they make for really complex code and
		// yield little advantage: if an attachment is linked to an interface
		// and 1 sec later it is updated, the association is no longer valid.
		// For this reason, this code does not attempt to always take the
		// optimal choice: in case of collisions the interface that was linked
		// first is kept, while the other is deleted. First, collisions should
		// be rare. Second, even if the wrong interface is kept, when the
		// connection agent starts normal operation (i.e. after this sync) it
		// will process the attachment linked to the interface, and it will
		// detect that their fields do not match. This will trigger deletion of
		// the interface and a new one which matches the attachment will be
		// created and linked to the attachment.
		if !ownerAlreadyHasInterface {
			ifc.linkToOwner(ifcOwner, ca)
			klog.V(3).Infof("Linked pre-existing network interface %s with attachment %s", ifc, ifcOwnerNSN)
			return nil
		}
	}

	// No attachment elegible to own the network interface was found: delete it.
	ca.deleteOrphanNetworkInterface(ifc)
	return nil
}

func (ca *ConnectionAgent) deleteOrphanNetworkInterface(ifc networkInterface) {
	for i := 1; ; i++ {
		err := ifc.delete(k8stypes.NamespacedName{}, ca)
		if err == nil {
			klog.V(4).Infof("Deleted pre-existing orphan network interface %s (attempt nbr. %d)", ifc, i)
			break
		}
		klog.Errorf("failed to delete pre-existing orphan network interface %s (attempt nbr. %d)", ifc, i)
		time.Sleep(netFabricRetryPeriod)
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

	err := ca.syncS2VNState(attNSN, att)
	if err != nil {
		return err
	}
	klog.V(3).Infof("Synced stage2VNState for attachment %s.", attNSN)

	// Create/update/delete the network interface of the NetworkAttachment.
	ifc, statusErrs, err := ca.syncNetworkInterface(attNSN, att)
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
	localIfc := ifc.(*localNetworkInterface)
	ifcMAC := localIfc.GuestMAC.String()
	ifcPCER := localIfc.postCreateExecReport.getReport()
	if ca.localAttachmentIsUpToDate(att, ifcMAC, localIfc.Name, statusErrs, ifcPCER) {
		return nil
	}

	return ca.updateLocalAttachmentStatus(att, ifcMAC, localIfc.Name, statusErrs, ifcPCER)
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
	attLister, moreThanOneLister := ca.getLister(attNSN)

	if moreThanOneLister {
		// If more than one lister was found the NetworkAttachment was seen in
		// more than one informer and the most up-to-date version is unkown.
		// Halt processing until future delete notifications from the informers
		// storing stale versions arrive and reveal the current state of the
		// NetworkAttachment.
		klog.V(4).Infof("Cannot process NetworkAttachment %s because it was seen in more than one informer.", attNSN)
		haltProcessing = true
		return
	}

	if attLister == nil {
		// No lister for the NetworkAttachment was found, hence it's in no
		// informer: it must have been deleted.
		return
	}

	// Retrieve the NetworkAttachment.
	// ? What happens if this .Get hits the cache after the associated Informer
	// has been stopped? I suspect nothing worth special care, but double-check
	// to make sure.
	att, err := attLister.Get(attNSN.Name)
	if err != nil && !k8serrors.IsNotFound(err) {
		klog.Errorf("Failed to look up NetworkAttachment %s: %s. This should never happen, there will be no retry.", attNSN, err.Error())
		haltProcessing = true
	}

	return
}

func (ca *ConnectionAgent) getLister(att k8stypes.NamespacedName) (lister koslisterv1a1.NetworkAttachmentNamespaceLister, moreThanOneVNI bool) {
	ca.s1VirtNetsState.RLock()
	defer ca.s1VirtNetsState.RUnlock()

	attVNIs := ca.s1VirtNetsState.attToVNIs[att]

	if len(attVNIs) > 1 {
		moreThanOneVNI = true
		return
	}

	if len(attVNIs) == 0 {
		return
	}

	var attVNI uint32
	for attVNI = range attVNIs {
	}

	if attVNI == localAttsInformerFakeVNI {
		lister = ca.localAttsLister.NetworkAttachments(att.Namespace)
	} else {
		attStage1VNState := ca.s1VirtNetsState.vniToVNState[attVNI]
		lister = attStage1VNState.remoteAttsLister
	}
	return
}

func (ca *ConnectionAgent) syncS2VNState(attNSN k8stypes.NamespacedName, att *netv1a1.NetworkAttachment) error {
	ca.s2VirtNetsState.Lock()
	defer ca.s2VirtNetsState.Unlock()

	attOldStage2VNI, attOldStage2VNIFound := ca.s2VirtNetsState.localAttToStage2VNI[attNSN]
	if attOldStage2VNIFound && (att == nil || attOldStage2VNI != att.Status.AddressVNI || ca.node != att.Spec.Node) {
		// The NetworkAttachment was local and recorded in a
		// stage2VirtualNetworkState, but now it should no longer be there
		// because its state has changed: remove it.
		ca.removeLocalAttFromS2VNState(attNSN, attOldStage2VNI)
	}

	if att != nil && ca.node == att.Spec.Node && (!attOldStage2VNIFound || attOldStage2VNI != att.Status.AddressVNI) {
		// The NetworkAttachment is local and is not in the
		// stage2VirtualNetworkState of its virtual network yet: add it.
		return ca.addLocalAttToS2VNState(attNSN, att.Status.AddressVNI)
	}

	return nil
}

// addLocalAttToStage2VNState adds a local NetworkAttachment to its
// stage2VirtualNetworkState and inits such state if the NetworkAttachment is
// the first local one (this entails initializing the stage1VirtualNetworkState
// as well).
func (ca *ConnectionAgent) addLocalAttToS2VNState(att k8stypes.NamespacedName, vni uint32) error {
	attS2VNState := ca.s2VirtNetsState.vniToVNState[vni]
	if attS2VNState == nil {
		// The NetworkAttachment is the first local one for its virtual network,
		// which has therefore just become relevant.
		attS2VNState = ca.initStage2VNState(vni, att.Namespace)
		klog.V(2).Infof("Virtual Network with VNI %d became relevant because of creation of first local attachment %s. Its state has been initialized.", vni, att)
	}

	if attS2VNState.namespace != att.Namespace {
		// If the NetworkAttachment's namespace does not match the one of the
		// stage2VirtNetState for its vni X a virtual network with vni X must
		// have been deleted (AKA all its subnets have been) right before a new
		// one with the same vni but different namespace was created, but the
		// connection agent has not processed all the notifications yet.
		// Return an error to trigger delayed reprocessing, when (hopefully)
		// all the notifications have been processed.
		return fmt.Errorf("attachment is local but could not be added to stage2VirtualNetworkState because namespace found there (%s) does not match the attachment's", attS2VNState.namespace)
	}

	ca.s2VirtNetsState.localAttToStage2VNI[att] = vni
	attS2VNState.localAtts[att.Name] = struct{}{}
	return nil
}

// removeLocalAttFromStage2VNState removes a local NetworkAttachment from its
// stage2VirtualNetworkState and clears such state if the NetworkAttachment was
// the last local one (this entails clearing the stage1VirtualNetworkState as well).
// Invoke only while holding ca.s2VirtNetsState's mutex.
func (ca *ConnectionAgent) removeLocalAttFromS2VNState(att k8stypes.NamespacedName, vni uint32) {
	oldStage2VNState := ca.s2VirtNetsState.vniToVNState[vni]
	delete(oldStage2VNState.localAtts, att.Name)
	delete(ca.s2VirtNetsState.localAttToStage2VNI, att)

	if len(oldStage2VNState.localAtts) == 0 {
		// Clear all resources associated with the virtual network because the
		// last local NetworkAttachment in it has been deleted and it has thus
		// become irrelevant.
		delete(ca.s2VirtNetsState.vniToVNState, vni)
		close(oldStage2VNState.remoteAttsInformerStopCh)
		ca.clearStage1VNState(vni, oldStage2VNState.namespace)
		klog.V(2).Infof("Virtual Network with VNI %d became irrelevant because of deletion of last local attachment %s. Its state has been cleared.", vni, att)
	}
}

// initStage2VNState configures and starts the Informer for remote
// NetworkAttachments in the virtual network identified by `vni`.
// It also initializes the stage1VirtualNetworkState corresponding to `vni`.
func (ca *ConnectionAgent) initStage2VNState(vni uint32, namespace string) *stage2VirtualNetworkState {
	remAttsInformer, remAttsLister := ca.newInformerAndLister(resyncPeriod, namespace, ca.remoteAttSelector(vni), attHostIPAndIP)
	newStage2VNState := &stage2VirtualNetworkState{
		namespace:                namespace,
		localAtts:                make(map[string]struct{}, 1),
		remoteAttsInformer:       remAttsInformer,
		remoteAttsInformerStopCh: make(chan struct{}),
	}
	ca.s2VirtNetsState.vniToVNState[vni] = newStage2VNState

	s1VNS := ca.initStage1VNState(vni, remAttsLister.NetworkAttachments(namespace))

	remAttsInformer.AddEventHandler(ca.newRemoteAttsEventHandler(s1VNS))
	go remAttsInformer.Run(mergeStopChannels(ca.stopCh, newStage2VNState.remoteAttsInformerStopCh))

	return newStage2VNState
}

func (ca *ConnectionAgent) initStage1VNState(vni uint32, remAttsLister koslisterv1a1.NetworkAttachmentNamespaceLister) *stage1VirtualNetworkState {
	ca.s1VirtNetsState.Lock()
	defer ca.s1VirtNetsState.Unlock()

	s1VNS := &stage1VirtualNetworkState{
		remoteAtts:       make(map[string]struct{}),
		remoteAttsLister: remAttsLister}
	ca.s1VirtNetsState.vniToVNState[vni] = s1VNS
	return s1VNS
}

func (ca *ConnectionAgent) clearStage1VNState(vni uint32, namespace string) {
	ca.s1VirtNetsState.Lock()
	defer ca.s1VirtNetsState.Unlock()

	stage1VNState := ca.s1VirtNetsState.vniToVNState[vni]
	delete(ca.s1VirtNetsState.vniToVNState, vni)
	for aRemoteAtt := range stage1VNState.remoteAtts {
		aRemoteAttNSN := k8stypes.NamespacedName{Namespace: namespace,
			Name: aRemoteAtt}
		aRemoteAttVNIs := ca.s1VirtNetsState.attToVNIs[aRemoteAttNSN]
		delete(aRemoteAttVNIs, vni)
		if len(aRemoteAttVNIs) == 0 {
			delete(ca.s1VirtNetsState.attToVNIs, aRemoteAttNSN)
		}
		ca.queue.Add(aRemoteAttNSN)
	}
}

func (ca *ConnectionAgent) newRemoteAttsEventHandler(s1VNS *stage1VirtualNetworkState) k8scache.ResourceEventHandlerFuncs {
	onRemoteAttAdd := func(obj interface{}) {
		att := obj.(*netv1a1.NetworkAttachment)
		klog.V(5).Infof("Remote NetworkAttachments informer for VNI %06x: notified of addition of %#+v", att.Status.AddressVNI, att)

		attNSN := parse.AttNSN(att)
		added := ca.updateS1VNState(attNSN, att.Status.AddressVNI, s1VNS, true)
		if added {
			ca.queue.Add(attNSN)
		}
	}

	onRemoteAttUpdate := func(oldObj, obj interface{}) {
		oldAtt, att := oldObj.(*netv1a1.NetworkAttachment), obj.(*netv1a1.NetworkAttachment)

		// The only fields affecting remote network interfaces handling that can
		// be seen changing by this function are status.ipv4 and status.hostIP,
		// so enqueue only if they changed.
		if oldAtt.Status.IPv4 != att.Status.IPv4 || oldAtt.Status.HostIP != att.Status.HostIP {
			klog.V(5).Infof("Remote NetworkAttachments informer for VNI %06x: notified of update from %#+v to %#+v. Relevant state changed, the attachment will be reprocessed.", att.Status.AddressVNI, oldAtt, att)
			ca.queue.Add(parse.AttNSN(att))
		} else {
			klog.V(5).Infof("Remote NetworkAttachments informer for VNI %06x: notified of update from %#+v to %#+v. The update will be ignored because nothing relevant changed.", att.Status.AddressVNI, oldAtt, att)
		}
	}

	onRemoteAttDelete := func(obj interface{}) {
		att := parse.Peel(obj).(*netv1a1.NetworkAttachment)
		klog.V(5).Infof("Remote NetworkAttachments informer for VNI %06x: notified of deletion of %#+v", att.Status.AddressVNI, att)

		attNSN := parse.AttNSN(att)
		removed := ca.updateS1VNState(attNSN, att.Status.AddressVNI, s1VNS, false)
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

func (ca *ConnectionAgent) updateS1VNState(att k8stypes.NamespacedName, vni uint32, attS1VNState *stage1VirtualNetworkState, attExists bool) (s1VNStateUpdated bool) {
	ca.s1VirtNetsState.Lock()
	defer ca.s1VirtNetsState.Unlock()

	// vniS1VNState will always be nil if `att` is local because there's no
	// stage1VirtualNetworkState for local attachments; it might be non-nil
	// if `att`is remote.
	vniS1VNState := ca.s1VirtNetsState.vniToVNState[vni]

	// If `att` is local this check is always false. If `att` is remote this
	// check is needed because this function executes within a notification
	// handler bound to an informer associated to a stage1VirtualNetworkState
	// (attS1VNState). If the handler executes AFTER the stage1VirtualNetworkState
	// is cleared due to becoming irrelevant, this function must not update the
	// stage1VirtualNetworkState associated to vni (vniS1VNState), because such
	// state either no longer exists or is for a newer virtual network with the
	// same vni as the handler's. This check detects such cases.
	if vniS1VNState != attS1VNState {
		return
	}

	attVNIs := ca.s1VirtNetsState.attToVNIs[att]
	if attExists {
		if attVNIs == nil {
			attVNIs = make(map[uint32]struct{}, 1)
			ca.s1VirtNetsState.attToVNIs[att] = attVNIs
		}
		attVNIs[vni] = struct{}{}
		if attS1VNState != nil {
			// attS1VNState is non-nil only for remote attachments.
			attS1VNState.remoteAtts[att.Name] = struct{}{}
		}
	} else {
		delete(attVNIs, vni)
		if len(attVNIs) == 0 {
			delete(ca.s1VirtNetsState.attToVNIs, att)
		}
		if attS1VNState != nil {
			// attS1VNState is non-nil only for remote attachments.
			delete(attS1VNState.remoteAtts, att.Name)
		}
	}

	s1VNStateUpdated = true
	return
}

func (ca *ConnectionAgent) syncNetworkInterface(attNSN k8stypes.NamespacedName, att *netv1a1.NetworkAttachment) (ifc networkInterface, statusErrs sliceOfString, err error) {
	oldIfc, oldIfcFound := ca.getNetworkInterface(attNSN)
	oldIfcCanBeUsed := oldIfcFound && oldIfc.canBeOwnedBy(att, ca.node)

	if oldIfcFound && !oldIfcCanBeUsed {
		err = oldIfc.delete(attNSN, ca)
		if err != nil {
			return
		}
		ca.unassignNetworkInterface(attNSN)
		klog.V(4).Infof("Deleted network interface %s for attachment %s", oldIfc, attNSN)
	}

	if att == nil {
		return
	}

	if oldIfcCanBeUsed {
		if oldLocalIfc, oldIfcIsLocal := oldIfc.(*localNetworkInterface); oldIfcIsLocal {
			statusErrs = ca.launchCommand(attNSN, oldLocalIfc.LocalNetIfc, att.Spec.PostCreateExec, nil, "postCreate", false)
			ifc = oldLocalIfc
		}
		klog.V(4).Infof("Attachment %s can use old network interface %s.", attNSN, oldIfc)
		return
	}

	if att.Spec.Node == ca.node {
		ifc, statusErrs, err = ca.createLocalNetworkInterface(att)
	} else {
		ifc, err = ca.createRemoteNetworkInterface(att)
	}
	if err == nil {
		ca.assignNetworkInterface(attNSN, ifc)
		klog.V(4).Infof("Created network interface %s for attachment %s", ifc, attNSN)
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

func (ca *ConnectionAgent) unassignNetworkInterface(att k8stypes.NamespacedName) {
	ca.attToNetworkInterfaceMutex.Lock()
	defer ca.attToNetworkInterfaceMutex.Unlock()

	delete(ca.attToNetworkInterface, att)
}

// localAttSelector returns a fields selector that matches local
// NetworkAttachments for whom a network interface can be created.
func (ca *ConnectionAgent) localAttSelector() k8sfields.Selector {
	// The NetworkAttachment must be local.
	localAtt := k8sfields.OneTermEqualSelector(attNodeField, ca.node)

	// The NetworkAttachment must have a virtual IP to create a network
	// interface.
	attWithAnIP := k8sfields.OneTermNotEqualSelector(attIPv4Field, "")

	// Return a selector given by the logical AND between localAtt and
	// attWithAnIP.
	return k8sfields.AndSelectors(localAtt, attWithAnIP)
}

// remoteAttSelector returns a fields selector that matches remote
// NetworkAttachments in the virtual network identified by `vni` for whom a
// network interface can be created.
func (ca *ConnectionAgent) remoteAttSelector(vni uint32) k8sfields.Selector {
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
	return k8sfields.AndSelectors(remoteAtt, attInSpecificVN, attWithAnIP, attWithHostIP)
}

func (ca *ConnectionAgent) newInformerAndLister(resyncPeriod time.Duration, ns string, fs k8sfields.Selector, indexer k8scache.IndexFunc) (k8scache.SharedIndexInformer, koslisterv1a1.NetworkAttachmentLister) {
	tloFunc := fsToTweakListOptionsFunc(fs)
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
// only one goroutine, so it accesses a stage2VirtualNetworkState without
// holding the appropriate mutex.
func (ca *ConnectionAgent) getRemoteAttsIndexer(vni uint32) k8scache.Indexer {
	s2VNState := ca.s2VirtNetsState.vniToVNState[vni]
	if s2VNState != nil {
		return s2VNState.remoteAttsInformer.GetIndexer()
	}
	return nil
}
