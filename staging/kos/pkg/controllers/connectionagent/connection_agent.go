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
	"bytes"
	"fmt"
	gonet "net"
	"net/http"
	"strconv"
	"strings"
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
	kosinternalifcs "k8s.io/examples/staging/kos/pkg/client/informers/externalversions/internalinterfaces"
	kosinformersv1a1 "k8s.io/examples/staging/kos/pkg/client/informers/externalversions/network/v1alpha1"
	koslisterv1a1 "k8s.io/examples/staging/kos/pkg/client/listers/network/v1alpha1"
	netfabric "k8s.io/examples/staging/kos/pkg/networkfabric"
	"k8s.io/examples/staging/kos/pkg/util/parse"
)

const (
	// Name of the indexer which computes a string concatenating the VNI and IP
	// of a network attachment. Used for syncing pre-existing interfaces at
	// start-up.
	attVNIAndIPIndexerName = "attachmentVNIAndIP"

	// NetworkAttachments in network.example.com/v1alpha1
	// fields names. Used to build field selectors.
	attNode   = "spec.node"
	attIPv4   = "status.ipv4"
	attHostIP = "status.hostIP"
	attVNI    = "status.addressVNI"

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
	localNodeName string
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
	vniToVnStateMutex sync.RWMutex
	vniToVnState      map[uint32]*vnState

	// nsnToVNStateVNI maps local attachments namespaced names to the VNI of the
	// vnState they're stored in. Accessed only while holding nsnToVNStateVNIMutex.
	nsnToVNStateVNIMutex sync.RWMutex
	nsnToVNStateVNI      map[k8stypes.NamespacedName]uint32

	nsnToLocalStateMutex      sync.RWMutex
	nsnToLocalMainState       map[k8stypes.NamespacedName]LocalAttachmentMainState
	nsnToPostCreateExecReport map[k8stypes.NamespacedName]*netv1a1.ExecReport

	nsnToRemoteIfcMutex sync.RWMutex
	nsnToRemoteIfc      map[k8stypes.NamespacedName]netfabric.RemoteNetIfc

	// nsnToVNIs maps attachments (both local and remote) namespaced names
	// to set of vnis where the attachments have been seen. It is accessed by the
	// notification handlers for remote attachments, which add/remove the vni
	// with which they see the attachment upon creation/deletion of the attachment
	// respectively. When a worker processes an attachment reference, it reads
	// from nsnToVNIs the vnis with which the attachment has been seen. If there's
	// more than one vni, the current state of the attachment is ambiguous and
	// the worker stops the processing. The deletion notification handler for one
	// of the VNIs of the attachment will cause the reference to be requeued
	// and hopefully by the time it is dequeued again the ambiguity as for the
	// current attachment state has been resolved. Accessed only while holding
	// nsnToVNIsMutex.
	nsnToVNIsMutex sync.RWMutex
	nsnToVNIs      map[k8stypes.NamespacedName]map[uint32]struct{}

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
func New(localNodeName string,
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
			ConstLabels: map[string]string{"node": localNodeName},
		})
	attachmentCreateToRemoteIfcHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "attachment_create_to_remote_ifc_latency_seconds",
			Help:        "Seconds from attachment CreationTimestamp to finished creating remote interface",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": localNodeName},
		})
	fabricLatencyHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "fabric_latency_seconds",
			Help:        "Network fabric operation time in seconds",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16},
			ConstLabels: map[string]string{"node": localNodeName},
		},
		[]string{"op", "err"})
	attachmentCreateToStatusHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "attachment_create_to_status_latency_seconds",
			Help:        "Seconds from attachment CreationTimestamp to return from successful status update",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": localNodeName},
		})
	attachmentStatusHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "attachment_status_latency_seconds",
			Help:        "Round trip latency to update attachment status, in seconds",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": localNodeName},
		},
		[]string{"statusErr", "err"})
	localAttachmentsGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "local_attachments",
			Help:        "Number of local attachments in network fabric",
			ConstLabels: map[string]string{"node": localNodeName},
		})
	remoteAttachmentsGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "remote_attachments",
			Help:        "Number of remote attachments in network fabric",
			ConstLabels: map[string]string{"node": localNodeName},
		})
	attachmentExecDurationHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "attachment_exec_duration_secs",
			Help:        "Time to run attachment commands, in seconds",
			Buckets:     []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256},
			ConstLabels: map[string]string{"node": localNodeName},
		},
		[]string{"what"})
	attachmentExecStatusCounts := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "attachment_exec_status_count",
			Help:        "Counts of commands by what and exit status",
			ConstLabels: map[string]string{"node": localNodeName},
		},
		[]string{"what", "exitStatus"})
	fabricNameCounts := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "fabric_count",
			Help:        "Indicator of chosen fabric implementation",
			ConstLabels: map[string]string{"node": localNodeName},
		},
		[]string{"fabric"})
	workerCount := prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace:   MetricsNamespace,
			Subsystem:   MetricsSubsystem,
			Name:        "worker_count",
			Help:        "Number of queue worker threads",
			ConstLabels: map[string]string{"node": localNodeName},
		})
	prometheus.MustRegister(attachmentCreateToLocalIfcHistogram, attachmentCreateToRemoteIfcHistogram, fabricLatencyHistograms, attachmentCreateToStatusHistogram, attachmentStatusHistograms, localAttachmentsGauge, remoteAttachmentsGauge, attachmentExecDurationHistograms, attachmentExecStatusCounts, fabricNameCounts, workerCount)

	fabricNameCounts.With(prometheus.Labels{"fabric": netFabric.Name()}).Inc()
	workerCount.Add(float64(workers))
	eventBroadcaster := k8seventrecord.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.V(3).Infof)
	eventBroadcaster.StartRecordingToSink(&k8scorev1client.EventSinkImpl{eventIfc})
	eventRecorder := eventBroadcaster.NewRecorder(kosscheme.Scheme, k8scorev1api.EventSource{Component: "connection-agent", Host: localNodeName})

	return &ConnectionAgent{
		localNodeName:                        localNodeName,
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
		nsnToVNIs:                            make(map[k8stypes.NamespacedName]map[uint32]struct{}),
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
	localAttWithAnIPSelector := ca.localAttWithAnIPSelector()

	ca.localAttsInformer, ca.localAttsLister = v1a1AttsCustomInformerAndLister(ca.kcs,
		resyncPeriod,
		fromFieldsSelectorToTweakListOptionsFunc(localAttWithAnIPSelector.String()))

	ca.localAttsInformer.AddIndexers(map[string]k8scache.IndexFunc{attVNIAndIPIndexerName: attVNIAndIPIndexer})

	ca.localAttsInformer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
		AddFunc:    ca.onLocalAttAdd,
		UpdateFunc: ca.onLocalAttUpdate,
		DeleteFunc: ca.onLocalAttRemove,
	})
}

func (ca *ConnectionAgent) onLocalAttAdd(obj interface{}) {
	att := obj.(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Local NetworkAttachments cache: notified of addition of %#+v", att)
	ca.queue.Add(parse.AttNSN(att))
}

func (ca *ConnectionAgent) onLocalAttUpdate(oldObj, obj interface{}) {
	oldAtt, att := oldObj.(*netv1a1.NetworkAttachment), obj.(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Local NetworkAttachments cache: notified of update from %#+v to %#+v", oldAtt, att)
	if oldAtt.Status.IPv4 != att.Status.IPv4 || oldAtt.Status.AddressVNI != att.Status.AddressVNI {
		// The only fields affecting local network interfaces handling that can
		// be seen changing by this function are Status.IPv4 and Status.AddressVNI.
		ca.queue.Add(parse.AttNSN(att))
	}
}

func (ca *ConnectionAgent) onLocalAttRemove(obj interface{}) {
	att := parse.Peel(obj).(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Local NetworkAttachments cache: notified of removal of %#+v", att)
	ca.queue.Add(parse.AttNSN(att))
}

func (ca *ConnectionAgent) syncPreExistingIfcs() error {
	if err := ca.syncPreExistingLocalIfcs(); err != nil {
		return err
	}

	return ca.syncPreExistingRemoteIfcs()
}

func (ca *ConnectionAgent) syncPreExistingLocalIfcs() error {
	allPreExistingLocalIfcs, err := ca.netFabric.ListLocalIfcs()
	if err != nil {
		return fmt.Errorf("failed initial local network interfaces list: %s", err.Error())
	}

	for _, aPreExistingLocalIfc := range allPreExistingLocalIfcs {
		ifcVNIAndIP := localIfcVNIAndIP(&aPreExistingLocalIfc)
		ifcOwnerAtts, err := ca.localAttsInformer.GetIndexer().ByIndex(attVNIAndIPIndexerName, ifcVNIAndIP)
		if err != nil {
			return fmt.Errorf("indexing local network interface with VNI/IP=%s failed: %s",
				ifcVNIAndIP,
				err.Error())
		}

		if len(ifcOwnerAtts) == 1 {
			// A local attachment which should own the interface because their
			// VNI and IP match was found.
			ifcOwnerAtt := ifcOwnerAtts[0].(*netv1a1.NetworkAttachment)
			nsn := parse.AttNSN(ifcOwnerAtt)
			oldLocalState, oldIfcExists := ca.getLocalAttState(nsn)
			ca.setLocalAttMainState(nsn, LocalAttachmentMainState{aPreExistingLocalIfc, ifcOwnerAtt.Spec.PostDeleteExec})
			ca.setExecReport(nsn, ifcOwnerAtt.Status.PostCreateExecReport)
			klog.V(3).Infof("Matched interface %#+v with local attachment %#+v", aPreExistingLocalIfc, ifcOwnerAtt)
			if oldIfcExists {
				aPreExistingLocalIfc = oldLocalState.LocalNetIfc
			} else {
				continue
			}
		}

		// The interface must be deleted, e.g. because it could not be matched
		// to an attachment, or because the attachment to which it has already
		// been matched has changed and was matched to a different interface.
		for i := 1; true; i++ {
			err := ca.DeleteLocalIfc(aPreExistingLocalIfc)
			if err == nil {
				break
			}
			klog.V(3).Infof("Deletion of orphan local interface %#+v failed: %s. Attempt nbr. %d",
				aPreExistingLocalIfc,
				err.Error(),
				i)
			time.Sleep(netFabricRetryPeriod)
		}
		// Do not run the PostDeleteExec, it probably would not work
		// because the PostCreateExec ran in a different container if
		// at all.
		klog.V(3).Infof("Deleted orphan local interface %#+v", aPreExistingLocalIfc)
	}

	return nil
}

func (ca *ConnectionAgent) syncPreExistingRemoteIfcs() error {
	// Start all remote attachments Informers because we need to look up remote
	// attachments to decide which interfaces to keep and which to delete.
	allLocalAtts, err := ca.localAttsLister.List(k8slabels.Everything())
	if err != nil {
		return fmt.Errorf("failed initial local attachments list: %s", err.Error())
	}
	for _, aLocalAtt := range allLocalAtts {
		nsn, attVNI := parse.AttNSN(aLocalAtt), aLocalAtt.Status.AddressVNI
		ca.updateVNStateForExistingAtt(nsn, true, attVNI)
	}

	// Read all remote interfaces, for each interface find the attachment with
	// the same VNI and IP in the Informer's cache for the remote attachments
	// with the same VNI as the interface. If either the attachment or the
	// Informer are not found, delete the interface, bind it to the attachment
	// otherwise.
	allPreExistingRemoteIfcs, err := ca.netFabric.ListRemoteIfcs()
	if err != nil {
		return fmt.Errorf("failed initial remote network interfaces list: %s", err.Error())
	}
	for _, aPreExistingRemoteIfc := range allPreExistingRemoteIfcs {
		var ifcOwnerAtts []interface{}
		ifcVNI := aPreExistingRemoteIfc.VNI
		remoteAttsInformer, remoteAttsInformerStopCh := ca.getRemoteAttsInformerForVNI(ifcVNI)
		if remoteAttsInformer != nil {
			if !remoteAttsInformer.HasSynced() &&
				!k8scache.WaitForCacheSync(remoteAttsInformerStopCh, remoteAttsInformer.HasSynced) {
				return fmt.Errorf("failed to sync cache of remote attachments for VNI %06x", ifcVNI)
			}
			ifcOwnerAtts, err = remoteAttsInformer.GetIndexer().ByIndex(attVNIAndIPIndexerName, remoteIfcVNIAndIP(&aPreExistingRemoteIfc))
		}

		if len(ifcOwnerAtts) == 1 {
			// A remote attachment which should own the interface because their
			// VNI and IP match was found.
			ifcOwnerAtt := ifcOwnerAtts[0].(*netv1a1.NetworkAttachment)
			nsn := parse.AttNSN(ifcOwnerAtt)
			oldRemoteIfc, oldRemoteIfcExists := ca.getRemoteIfc(nsn)
			ca.assignRemoteIfc(nsn, aPreExistingRemoteIfc)
			klog.V(3).Infof("Matched interface %#+v with remote attachment %#+v",
				aPreExistingRemoteIfc,
				ifcOwnerAtt)
			if oldRemoteIfcExists {
				aPreExistingRemoteIfc = oldRemoteIfc
			} else {
				if oldLocalState, oldLocalIfcExists := ca.getLocalAttState(nsn); oldLocalIfcExists {
					for i := 1; true; i++ {
						err := ca.DeleteLocalIfc(oldLocalState.LocalNetIfc)
						if err == nil {
							break
						}
						klog.V(3).Infof("Deletion of orphan local interface %#+v failed: %s. Attempt nbr. %d",
							oldLocalState.LocalNetIfc,
							err.Error(),
							i)
						time.Sleep(netFabricRetryPeriod)
					}
					ca.unsetLocalAttState(nsn)
					// Do not run the PostDeleteExec, it probably would not work
					// because the PostCreateExec ran in a different container
					// if at all.
					klog.V(3).Infof("Deleted orphan local interface %#+v", oldLocalState.LocalNetIfc)
				}
				continue
			}
		}

		// Either there's no remote attachments Informer associated with the
		// interface VNI (because there are no local attachments with that VNI),
		// or no remote attachment owning the interface was found, or the
		// attachment owning the interface already has one. For all such cases
		// we need to delete the interface.
		for i := 1; true; i++ {
			err := ca.DeleteRemoteIfc(aPreExistingRemoteIfc)
			if err == nil {
				break
			}
			klog.V(3).Infof("Deletion of orphan remote interface %#+v failed: %s. Attempt nbr. %d",
				aPreExistingRemoteIfc,
				err.Error(),
				i)
			time.Sleep(netFabricRetryPeriod)
		}
		klog.V(3).Infof("Deleted orphan remote interface %#+v", aPreExistingRemoteIfc)
	}

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
	att, deleted := ca.getAttachment(attNSN)
	if att != nil {
		// If we are here the attachment exists and it's current state is univocal
		return ca.processExistingAtt(att)
	} else if deleted {
		// If we are here the attachment has been deleted
		return ca.processDeletedAtt(attNSN)
	}
	return nil
}

// getAttachment attempts to determine the univocal version of the NetworkAttachment
// with namespaced name attNSN. If it succeeds it returns the attachment if it is
// found in an Informer cache or a boolean flag set to true if it could not be found
// in any cache (e.g. because it has been deleted). If the current attachment
// version cannot be determined without ambiguity, the attachment return value is nil,
// and the deleted flag is set to false. An attachment is considered amibguous if
// it either has been seen with more than one vni in a remote attachments cache,
// or if it is found both in the local attachments cache and a remote attachments
// cache.
func (ca *ConnectionAgent) getAttachment(attNSN k8stypes.NamespacedName) (*netv1a1.NetworkAttachment, bool) {
	// Retrieve the number of VN(I)s where the attachment could be as a remote
	// attachment, or, if it could be only in one VN(I), return that VNI.
	vni, nbrOfVNIs := ca.getAttSeenInVNI(attNSN)
	if nbrOfVNIs > 1 {
		// If the attachment could be a remote one in more than one VNI, we
		// return immediately. When a deletion notification handler removes the
		// VNI with which it's seeing the attachment the attachment state will be
		// "less ambiguous" (one less potential VNI) and a reference will be enqueued
		// again triggering reconsideration of the attachment.
		klog.V(4).Infof("Attachment %s has inconsistent state, found in %d VN(I)s",
			attNSN,
			nbrOfVNIs)
		return nil, false
	}

	// If the attachment has been seen in exactly one VNI lookup it up in
	// the remote attachments cache for the VNI with which it's been seen
	var (
		attAsRemote          *netv1a1.NetworkAttachment
		remAttCacheLookupErr error
	)
	if nbrOfVNIs == 1 {
		remoteAttsLister := ca.getRemoteAttListerForVNI(vni)
		if remoteAttsLister != nil {
			attAsRemote, remAttCacheLookupErr = remoteAttsLister.Get(attNSN.Name)
		}
	}

	// Lookup the attachment in the local attachments cache
	attAsLocal, localAttCacheLookupErr := ca.localAttsLister.NetworkAttachments(attNSN.Namespace).Get(attNSN.Name)

	switch {
	case (remAttCacheLookupErr != nil && !k8serrors.IsNotFound(remAttCacheLookupErr)) ||
		(localAttCacheLookupErr != nil && !k8serrors.IsNotFound(localAttCacheLookupErr)):
		// If we're here at least one of the two lookups failed. This should
		// never happen. No point in retrying.
		klog.V(1).Infof("Attempt to retrieve attachment %s with lister failed: %s. This should never happen, hence a reference to %s will not be requeued",
			attNSN,
			aggregateErrors("\n\t", remAttCacheLookupErr, localAttCacheLookupErr).Error(),
			attNSN)
	case attAsLocal != nil && attAsRemote != nil:
		// If we're here the attachment has been found in both caches, hence it's
		// state is ambiguous. It will be deleted by one of the caches soon, and
		// this will cause a reference to be enqueued, so it will be processed
		// again when the ambiguity has been resolved (assuming it has not been
		// seen with other VNIs meanwhile).
		klog.V(4).Infof("Att %s has inconsistent state: found both in local atts cache and remote atts cache for VNI %06x",
			attNSN,
			vni)
	case attAsLocal != nil && attAsRemote == nil:
		// If we're here the attachment was found only in the local cache:
		// that's the univocal version of the attachment
		return attAsLocal, false
	case attAsLocal == nil && attAsRemote != nil:
		// If we're here the attachment was found only in the remote attachments
		// cache for its vni: that's the univocal version of the attachment
		return attAsRemote, false
	}
	// If we're here neither lookup could find the attachment: we assume the
	// attachment has been deleted by both caches and is therefore no longer
	// relevant to the connection agent
	return nil, true
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
		ca.removeSeenInVNI(attNSN, attVNI)
		return ca.processDeletedAtt(attNSN)
	}

	// Create or update the interface associated with the attachment.
	var attHostIP gonet.IP
	if attNode == ca.localNodeName {
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
	if attNode != ca.localNodeName {
		return nil
	}
	// The attachment is local, so make sure its status reflects the
	// state held here
	localHostIPStr := ca.hostIP.String()
	if localHostIPStr != att.Status.HostIP ||
		ifcName != att.Status.IfcName ||
		macAddrS != att.Status.MACAddress ||
		!pcer.Equiv(att.Status.PostCreateExecReport) ||
		!statusErrs.Equal(SliceOfString(att.Status.Errors.Host)) {

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

	return ca.updateVNStateForExistingAtt(attNSN, attNode == ca.localNodeName, attNewVNI)
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
func (ca *ConnectionAgent) createOrUpdateIfc(attGuestIP, attHostIP gonet.IP, attVNI uint32, att *netv1a1.NetworkAttachment) (attMACStr, ifcName string, statusErrs SliceOfString, pcer *netv1a1.ExecReport, err error) {
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
			ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "CreateLocalIfc", "err": FormatErrVal(err != nil)}).Observe(tAfterFabric.Sub(tBeforeFabric).Seconds())
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
			ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "CreateRemoteIfc", "err": FormatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
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

func (ca *ConnectionAgent) setAttStatus(att *netv1a1.NetworkAttachment, macAddrS, ifcName string, statusErrs SliceOfString, pcer *netv1a1.ExecReport) (*netv1a1.NetworkAttachment, error) {
	att2 := att.DeepCopy()
	att2.Status.Errors.Host = statusErrs
	att2.Status.MACAddress = macAddrS
	att2.Status.HostIP = ca.hostIP.String()
	att2.Status.IfcName = ifcName
	att2.Status.PostCreateExecReport = pcer
	tBeforeStatus := time.Now()
	updatedAtt, err := ca.netv1a1Ifc.NetworkAttachments(att2.Namespace).Update(att2)
	tAfterStatus := time.Now()
	ca.attachmentStatusHistograms.With(prometheus.Labels{"statusErr": FormatErrVal(len(statusErrs) > 0), "err": FormatErrVal(err != nil)}).Observe(tAfterStatus.Sub(tBeforeStatus).Seconds())
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
		ca.removeSeenInVNI(aRemoteAttNSN, vni)
		ca.queue.Add(aRemoteAttNSN)
	}
}

func (ca *ConnectionAgent) initVNState(vni uint32, namespace string) *vnState {
	remoteAttsInformer, remoteAttsLister := v1a1AttsCustomNamespaceInformerAndLister(ca.kcs,
		resyncPeriod,
		namespace,
		fromFieldsSelectorToTweakListOptionsFunc(ca.remoteAttInVNWithVirtualIPHostIPSelector(vni).String()))

	remoteAttsInformer.AddIndexers(map[string]k8scache.IndexFunc{attVNIAndIPIndexerName: attVNIAndIPIndexer})

	remoteAttsInformer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
		AddFunc:    ca.onRemoteAttAdd,
		UpdateFunc: ca.onRemoteAttUpdate,
		DeleteFunc: ca.onRemoteAttRemove,
	})

	remoteAttsInformerStopCh := make(chan struct{})
	go remoteAttsInformer.Run(aggregateTwoStopChannels(ca.stopCh, remoteAttsInformerStopCh))

	return &vnState{
		remoteAttsInformer:       remoteAttsInformer,
		remoteAttsInformerStopCh: remoteAttsInformerStopCh,
		remoteAttsLister:         remoteAttsLister,
		namespace:                namespace,
		localAtts:                make(map[string]struct{}),
		remoteAtts:               make(map[string]struct{}),
	}
}

func (ca *ConnectionAgent) onRemoteAttAdd(obj interface{}) {
	att := obj.(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Remote NetworkAttachments cache for VNI %06x: notified of addition of %#+v", att.Status.AddressVNI, att)
	attNSN := parse.AttNSN(att)
	ca.addVNI(attNSN, att.Status.AddressVNI)
	ca.queue.Add(attNSN)
}

func (ca *ConnectionAgent) onRemoteAttUpdate(oldObj, obj interface{}) {
	oldAtt, att := oldObj.(*netv1a1.NetworkAttachment), obj.(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Remote NetworkAttachments cache for VNI %06x: notified of update from %#+v to %#+v.", att.Status.AddressVNI, oldAtt, att)
	if oldAtt.Status.IPv4 != att.Status.IPv4 || oldAtt.Status.HostIP != att.Status.HostIP {
		// The only fields affecting remote network interfaces handling that can
		// be seen changing by this function are Status.IPv4 and Status.HostIP.
		ca.queue.Add(parse.AttNSN(att))
	}
}

func (ca *ConnectionAgent) onRemoteAttRemove(obj interface{}) {
	att := parse.Peel(obj).(*netv1a1.NetworkAttachment)
	klog.V(5).Infof("Remote NetworkAttachments cache for VNI %06x: notified of deletion of %#+v", att.Status.AddressVNI, att)
	attNSN := parse.AttNSN(att)
	ca.removeSeenInVNI(attNSN, att.Status.AddressVNI)
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

func (ca *ConnectionAgent) addVNI(nsn k8stypes.NamespacedName, vni uint32) {
	ca.nsnToVNIsMutex.Lock()
	defer ca.nsnToVNIsMutex.Unlock()
	attVNIs := ca.nsnToVNIs[nsn]
	if attVNIs == nil {
		attVNIs = make(map[uint32]struct{})
		ca.nsnToVNIs[nsn] = attVNIs
	}
	attVNIs[vni] = struct{}{}
}

func (ca *ConnectionAgent) removeSeenInVNI(nsn k8stypes.NamespacedName, vni uint32) {
	ca.nsnToVNIsMutex.Lock()
	defer ca.nsnToVNIsMutex.Unlock()
	attVNIs := ca.nsnToVNIs[nsn]
	if attVNIs == nil {
		return
	}
	delete(attVNIs, vni)
	if len(attVNIs) == 0 {
		delete(ca.nsnToVNIs, nsn)
	}
}

func (ca *ConnectionAgent) getAttSeenInVNI(nsn k8stypes.NamespacedName) (onlyVNI uint32, nbrOfVNIs int) {
	ca.nsnToVNIsMutex.RLock()
	defer ca.nsnToVNIsMutex.RUnlock()
	attVNIs := ca.nsnToVNIs[nsn]
	nbrOfVNIs = len(attVNIs)
	if nbrOfVNIs == 1 {
		for onlyVNI = range attVNIs {
		}
	}
	return
}

func (ca *ConnectionAgent) getRemoteAttListerForVNI(vni uint32) koslisterv1a1.NetworkAttachmentNamespaceLister {
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

// localAttWithAnIPSelector returns a field selector that matches
// NetworkAttachments that run on the local node and have a virtual IP.
func (ca *ConnectionAgent) localAttWithAnIPSelector() k8sfields.Selector {
	// localAtt is a selector that expresses the constraint that the
	// NetworkAttachment runs on the same node as the connection agent.
	localAtt := k8sfields.OneTermEqualSelector(attNode, ca.localNodeName)

	// attWithAnIP is a selector that expresses the constraint that the
	// NetworkAttachment has a virtual IP.
	attWithAnIP := k8sfields.OneTermNotEqualSelector(attIPv4, "")

	// Return a selector that is a logical AND between attWithAnIP and localAtt
	return k8sfields.AndSelectors(localAtt, attWithAnIP)
}

// remoteAttInVNWithVirtualIPHostIPSelector returns a string representing a
// field selector that matches NetworkAttachments that run on a remote node on
// the Virtual Network identified by the given VNI and have a virtual IP and the
// host IP fields set.
func (ca *ConnectionAgent) remoteAttInVNWithVirtualIPHostIPSelector(vni uint32) k8sfields.Selector {
	// remoteAtt expresses the constraint that the NetworkAttachment runs on a
	// remote node.
	remoteAtt := k8sfields.OneTermNotEqualSelector(attNode, ca.localNodeName)

	// attWithAnIP and attWithHostIP express the constraints that the
	// NetworkAttachment has the fields storing virtual IP and host IP set, by
	// saying that such fields must not be equal to the empty string.
	attWithAnIP := k8sfields.OneTermNotEqualSelector(attIPv4, "")
	attWithHostIP := k8sfields.OneTermNotEqualSelector(attHostIP, "")

	// attInSpecificVN expresses the constraint that the NetworkAttachment
	// is in the Virtual Network identified by vni.
	attInSpecificVN := k8sfields.OneTermEqualSelector(attVNI, fmt.Sprint(vni))

	// Build and return a selector which is a logical AND between all the selectors
	// defined above.
	return k8sfields.AndSelectors(remoteAtt, attWithAnIP, attWithHostIP, attInSpecificVN)
}

func fromFieldsSelectorToTweakListOptionsFunc(customFieldSelector string) kosinternalifcs.TweakListOptionsFunc {
	return func(options *k8smetav1.ListOptions) {
		optionsFieldSelector := options.FieldSelector
		allSelectors := make([]string, 0, 2)
		if strings.Trim(optionsFieldSelector, " ") != "" {
			allSelectors = append(allSelectors, optionsFieldSelector)
		}
		allSelectors = append(allSelectors, customFieldSelector)
		options.FieldSelector = strings.Join(allSelectors, ",")
	}
}

func v1a1AttsCustomInformerAndLister(kcs *kosclientset.Clientset,
	resyncPeriod time.Duration,
	tweakListOptionsFunc kosinternalifcs.TweakListOptionsFunc) (k8scache.SharedIndexInformer, koslisterv1a1.NetworkAttachmentLister) {

	attv1a1Informer := createAttsv1a1Informer(kcs,
		resyncPeriod,
		k8smetav1.NamespaceAll,
		tweakListOptionsFunc)
	return attv1a1Informer.Informer(), attv1a1Informer.Lister()
}

func v1a1AttsCustomNamespaceInformerAndLister(kcs *kosclientset.Clientset,
	resyncPeriod time.Duration,
	namespace string,
	tweakListOptionsFunc kosinternalifcs.TweakListOptionsFunc) (k8scache.SharedIndexInformer, koslisterv1a1.NetworkAttachmentNamespaceLister) {

	attv1a1Informer := createAttsv1a1Informer(kcs,
		resyncPeriod,
		namespace,
		tweakListOptionsFunc)
	return attv1a1Informer.Informer(), attv1a1Informer.Lister().NetworkAttachments(namespace)
}

func createAttsv1a1Informer(kcs *kosclientset.Clientset,
	resyncPeriod time.Duration,
	namespace string,
	tweakListOptionsFunc kosinternalifcs.TweakListOptionsFunc) kosinformersv1a1.NetworkAttachmentInformer {

	localAttsInformerFactory := kosinformers.NewFilteredSharedInformerFactory(kcs,
		resyncPeriod,
		namespace,
		tweakListOptionsFunc)
	netv1a1Ifc := localAttsInformerFactory.Network().V1alpha1()
	return netv1a1Ifc.NetworkAttachments()
}

// attVNIAndIPIndexer is an Index function that computes a string made up by vni
// and IP of a NetworkAttachment. Used to sync pre-existing interfaces with
// attachments at start up.
func attVNIAndIPIndexer(obj interface{}) ([]string, error) {
	att := obj.(*netv1a1.NetworkAttachment)
	return []string{attVNIAndIP(att.Status.AddressVNI, att.Status.IPv4)}, nil
}

func attVNIAndIP(vni uint32, ipv4 string) string {
	return strconv.FormatUint(uint64(vni), 16) + "/" + ipv4
}

func localIfcVNIAndIP(ifc *netfabric.LocalNetIfc) string {
	return ifcVNIAndIP(ifc.VNI, ifc.GuestIP)
}

func remoteIfcVNIAndIP(ifc *netfabric.RemoteNetIfc) string {
	return ifcVNIAndIP(ifc.VNI, ifc.GuestIP)
}

func ifcVNIAndIP(vni uint32, ipv4 gonet.IP) string {
	return strconv.FormatUint(uint64(vni), 16) + "/" + ipv4.String()
}

func generateMACAddr(vni uint32, guestIPv4 gonet.IP) gonet.HardwareAddr {
	guestIPBytes := guestIPv4.To4()
	mac := make([]byte, 6, 6)
	mac[5] = byte(vni)
	mac[4] = byte(vni >> 8)
	mac[3] = guestIPBytes[3]
	mac[2] = guestIPBytes[2]
	mac[1] = guestIPBytes[1]
	mac[0] = (byte(vni>>13) & 0xF8) | ((guestIPBytes[0] & 0x02) << 1) | 2
	return mac
}

func generateIfcName(macAddr gonet.HardwareAddr) string {
	return "kos" + strings.Replace(macAddr.String(), ":", "", -1)
}

// aggregateStopChannels returns a channel which
// is closed when either ch1 or ch2 is closed
func aggregateTwoStopChannels(ch1, ch2 <-chan struct{}) chan struct{} {
	aggregateStopCh := make(chan struct{})
	go func() {
		select {
		case _, ch1Open := <-ch1:
			if !ch1Open {
				close(aggregateStopCh)
				return
			}
		case _, ch2Open := <-ch2:
			if !ch2Open {
				close(aggregateStopCh)
				return
			}
		}
	}()
	return aggregateStopCh
}

func aggregateErrors(sep string, errs ...error) error {
	aggregateErrsSlice := make([]string, 0, len(errs))
	for i, err := range errs {
		if err != nil && strings.Trim(err.Error(), " ") != "" {
			aggregateErrsSlice = append(aggregateErrsSlice, fmt.Sprintf("error nbr. %d ", i)+err.Error())
		}
	}
	if len(aggregateErrsSlice) > 0 {
		return fmt.Errorf("%s", strings.Join(aggregateErrsSlice, sep))
	}
	return nil
}

func ifcNeedsUpdate(ifcHostIP, newHostIP gonet.IP, ifcMAC, newMAC gonet.HardwareAddr) bool {
	return !ifcHostIP.Equal(newHostIP) || !bytes.Equal(ifcMAC, newMAC)
}

func (ca *ConnectionAgent) DeleteLocalIfc(ifc netfabric.LocalNetIfc) error {
	tBefore := time.Now()
	err := ca.netFabric.DeleteLocalIfc(ifc)
	tAfter := time.Now()
	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "DeleteLocalIfc", "err": FormatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
	return err
}

func (ca *ConnectionAgent) DeleteRemoteIfc(ifc netfabric.RemoteNetIfc) error {
	tBefore := time.Now()
	err := ca.netFabric.DeleteRemoteIfc(ifc)
	tAfter := time.Now()
	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "DeleteRemoteIfc", "err": FormatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
	return err
}

func FormatErrVal(err bool) string {
	if err {
		return "err"
	}
	return "ok"
}
