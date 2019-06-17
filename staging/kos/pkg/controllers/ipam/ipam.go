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

package ipam

import (
	"errors"
	"fmt"
	gonet "net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	k8scorev1api "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	k8sutilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8swait "k8s.io/apimachinery/pkg/util/wait"
	k8scorev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	k8scache "k8s.io/client-go/tools/cache"
	k8seventrecord "k8s.io/client-go/tools/record"
	k8sworkqueue "k8s.io/client-go/util/workqueue"

	netv1a1 "k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
	kosscheme "k8s.io/examples/staging/kos/pkg/client/clientset/versioned/scheme"
	kosclientv1a1 "k8s.io/examples/staging/kos/pkg/client/clientset/versioned/typed/network/v1alpha1"
	netlistv1a1 "k8s.io/examples/staging/kos/pkg/client/listers/network/v1alpha1"
	"k8s.io/examples/staging/kos/pkg/util/convert"
	"k8s.io/examples/staging/kos/pkg/util/parse"

	"k8s.io/examples/staging/kos/pkg/uint32set"
)

const (
	owningAttachmentIdxName = "owningAttachment"
	attachmentSubnetIdxName = "subnet"

	opCreation = "creation"
	opUpdate   = "update"
	opDeletion = "deletion"

	// The HTTP port under which the scraping endpoint ("/metrics") is served.
	// See https://github.com/prometheus/prometheus/wiki/Default-port-allocations .
	MetricsAddr = ":9295"

	// The HTTP path under which the scraping endpoint ("/metrics") is served.
	MetricsPath = "/metrics"

	// The namespace and subsystem of the Prometheus metrics produced here
	MetricsNamespace = "kos"
	MetricsSubsystem = "ipam"

	fullSubnetErrMsgPrefix = "no IP address available in subnet"
	fullSubnetStatusMsg    = "Referenced subnet has run out of IPs"
)

type IPAMController struct {
	netIfc         kosclientv1a1.NetworkV1alpha1Interface
	subnetInformer k8scache.SharedInformer
	subnetLister   netlistv1a1.SubnetLister
	netattInformer k8scache.SharedIndexInformer
	netattLister   netlistv1a1.NetworkAttachmentLister
	lockInformer   k8scache.SharedIndexInformer
	lockLister     netlistv1a1.IPLockLister
	eventRecorder  k8seventrecord.EventRecorder
	queue          k8sworkqueue.RateLimitingInterface
	workers        int
	attsMutex      sync.Mutex
	atts           map[k8stypes.NamespacedName]*NetworkAttachmentData
	addrCacheMutex sync.Mutex
	addrCache      map[uint32]uint32set.UInt32SetChooser

	// IPLock.CreationTimestamp - NetworkAttachment.CreationTimestamp
	attachmentCreateToLockHistogram prometheus.Histogram

	// round trip time to create an IPLock object
	lockOpHistograms *prometheus.HistogramVec

	// Attachment ObjectMeta.CreationTimestamp to return from status update
	attachmentCreateToAddressHistogram prometheus.Histogram

	// round trip time to update attachment status
	attachmentUpdateHistograms *prometheus.HistogramVec

	// Kind of anticipation use (0, 1, or 2)
	anticipationUsedHistogram prometheus.Histogram

	// Was the IP address in the Status not in the cache (0 or 1)?
	statusUsedHistogram prometheus.Histogram
}

// NetworkAttachmentData holds the local state for a
// NetworkAttachment.  The fields can only be accessed by a worker
// thread working on the NetworkAttachment.  The data for a given
// attachment is used to remember a status update while it is in
// flight. When the attachment's ResourceVersion is either
// anticipatingResourceVersion or anticiaptedResourceVersion,
// anticipationSubnetRV is the ResourceVersion of the attachment's
// subnet, and anticipatedIPv4 != nil then that address has been
// chosen based on that subnet revision and written into the
// attachment's status and there exists an IPLock that supports this,
// even if this controller has not yet been notified about that lock;
// when any other ResourceVersion is seen these three fields get set
// to their zero value.
type NetworkAttachmentData struct {
	anticipatedIPv4             gonet.IP
	anticipatingResourceVersion string
	anticipatedResourceVersion  string
	anticipationSubnetRV        string
}

func NewController(netIfc kosclientv1a1.NetworkV1alpha1Interface,
	subnetInformer k8scache.SharedInformer,
	subnetLister netlistv1a1.SubnetLister,
	netattInformer k8scache.SharedIndexInformer,
	netattLister netlistv1a1.NetworkAttachmentLister,
	lockInformer k8scache.SharedIndexInformer,
	lockLister netlistv1a1.IPLockLister,
	eventIfc k8scorev1client.EventInterface,
	queue k8sworkqueue.RateLimitingInterface,
	workers int,
	hostname string) *IPAMController {

	attachmentCreateToLockHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsSubsystem,
			Name:      "attachment_create_to_lock_latency_seconds",
			Help:      "Latency from Attachment CreationTimestamp to IPLock CreationTimestamp, in seconds",
			Buckets:   []float64{-1, 0, 1, 2, 3, 4, 6, 8, 12, 16, 24, 32, 64},
		})

	lockOpHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsSubsystem,
			Name:      "ip_lock_latency_seconds",
			Help:      "Round trip latency to create/delete IPLock object, in seconds",
			Buckets:   []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64},
		},
		[]string{"op", "err"})

	attachmentCreateToAddressHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsSubsystem,
			Name:      "attachment_create_to_address_latency_seconds",
			Help:      "Latency from attachment CreationTimestamp to return from status update, in seconds",
			Buckets:   []float64{-1, 0, 1, 2, 3, 4, 6, 8, 12, 16, 24, 32, 64},
		})

	attachmentUpdateHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsSubsystem,
			Name:      "attachment_update_latency_seconds",
			Help:      "Round trip latency to set attachment address, in seconds",
			Buckets:   []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64},
		},
		[]string{"statusErr", "err"})

	anticipationUsedHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsSubsystem,
			Name:      "anticipation_used",
			Help:      "Kind of anticipation use",
			Buckets:   []float64{0, 1, 2},
		})

	statusUsedHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: MetricsNamespace,
			Subsystem: MetricsSubsystem,
			Name:      "status_used",
			Help:      "Was the IP address in Status used?",
			Buckets:   []float64{0, 1},
		})

	prometheus.MustRegister(attachmentCreateToLockHistogram, lockOpHistograms, attachmentCreateToAddressHistogram, attachmentUpdateHistograms, anticipationUsedHistogram, statusUsedHistogram)

	eventBroadcaster := k8seventrecord.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.V(3).Infof)
	eventBroadcaster.StartRecordingToSink(&k8scorev1client.EventSinkImpl{eventIfc})
	eventRecorder := eventBroadcaster.NewRecorder(kosscheme.Scheme, k8scorev1api.EventSource{Component: "ipam", Host: hostname})

	netattInformer.AddIndexers(map[string]k8scache.IndexFunc{attachmentSubnetIdxName: AttachmentSubnets})
	lockInformer.AddIndexers(map[string]k8scache.IndexFunc{owningAttachmentIdxName: OwningAttachments})

	return &IPAMController{
		netIfc:                             netIfc,
		subnetInformer:                     subnetInformer,
		subnetLister:                       subnetLister,
		netattInformer:                     netattInformer,
		netattLister:                       netattLister,
		lockInformer:                       lockInformer,
		lockLister:                         lockLister,
		eventRecorder:                      eventRecorder,
		queue:                              queue,
		workers:                            workers,
		atts:                               make(map[k8stypes.NamespacedName]*NetworkAttachmentData),
		addrCache:                          make(map[uint32]uint32set.UInt32SetChooser),
		attachmentCreateToLockHistogram:    attachmentCreateToLockHistogram,
		lockOpHistograms:                   lockOpHistograms,
		attachmentCreateToAddressHistogram: attachmentCreateToAddressHistogram,
		attachmentUpdateHistograms:         attachmentUpdateHistograms,
		anticipationUsedHistogram:          anticipationUsedHistogram,
		statusUsedHistogram:                statusUsedHistogram,
	}
}

func (ctlr *IPAMController) Run(stopCh <-chan struct{}) error {
	defer k8sutilruntime.HandleCrash()
	defer ctlr.queue.ShutDown()

	glog.Info("Starting IPAM controller.")
	defer glog.Info("Shutting down IPAM controller.")

	// Serve Prometheus metrics
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		glog.Errorf("In-process HTTP server crashed: %s\n", http.ListenAndServe(MetricsAddr, nil).Error())
	}()

	ctlr.subnetInformer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
		ctlr.OnSubnetCreate,
		ctlr.OnSubnetUpdate,
		ctlr.OnSubnetDelete})
	ctlr.netattInformer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
		ctlr.OnAttachmentCreate,
		ctlr.OnAttachmentUpdate,
		ctlr.OnAttachmentDelete})
	ctlr.lockInformer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
		ctlr.OnLockCreate,
		ctlr.OnLockUpdate,
		ctlr.OnLockDelete})

	if !k8scache.WaitForCacheSync(stopCh, ctlr.subnetInformer.HasSynced, ctlr.lockInformer.HasSynced, ctlr.netattInformer.HasSynced) {
		return errors.New("informers' caches failed to sync")
	}
	glog.V(2).Info("Informers' caches synced.")
	for i := 0; i < ctlr.workers; i++ {
		go k8swait.Until(ctlr.processQueue, time.Second, stopCh)
	}
	glog.V(4).Infof("Launched %d workers\n", ctlr.workers)

	<-stopCh

	return nil
}

func (ctlr *IPAMController) OnSubnetCreate(obj interface{}) {
	subnet := obj.(*netv1a1.Subnet)
	ctlr.OnSubnetNotify(subnet, opCreation)
}

func (ctlr *IPAMController) OnSubnetUpdate(oldObj, newObj interface{}) {
	subnet := newObj.(*netv1a1.Subnet)
	ctlr.OnSubnetNotify(subnet, opUpdate)
}

func (ctlr *IPAMController) OnSubnetDelete(obj interface{}) {
	subnet := obj.(*netv1a1.Subnet)
	ctlr.OnSubnetNotify(subnet, opDeletion)
}

func (ctlr *IPAMController) OnSubnetNotify(subnet *netv1a1.Subnet, op string) {
	if op != opDeletion && !subnet.Status.Validated && len(subnet.Status.Errors.Validation) == 0 {
		glog.V(4).Infof("Notified of %s of Subnet %s/%s, taking no action because subnet has not been validated yet.", op, subnet.Namespace, subnet.Name)
		return
	}
	indexer := ctlr.netattInformer.GetIndexer()
	subnetAttachments, err := indexer.ByIndex(attachmentSubnetIdxName, subnet.Name)
	if err != nil {
		glog.Errorf("NetworkAttachment indexer .ByIndex(%q, %q) failed: %s\n", attachmentSubnetIdxName, subnet.Name, err.Error())
		return
	}
	glog.V(4).Infof("Notified of %s of Subnet %s/%s, queuing %d attachments\n", op, subnet.Namespace, subnet.Name, len(subnetAttachments))
	for _, attObj := range subnetAttachments {
		att := attObj.(*netv1a1.NetworkAttachment)
		ctlr.queue.Add(parse.AttNSN(att))
		glog.V(5).Infof("Queuing %s/%s due to notification of %s of Subnet %s/%s\n", att.Namespace, att.Name, op, subnet.Namespace, subnet.Name)
	}
}

func (ctlr *IPAMController) OnAttachmentCreate(obj interface{}) {
	att := obj.(*netv1a1.NetworkAttachment)
	glog.V(5).Infof("Notified of creation of NetworkAttachment %#+v\n", att)
	ctlr.queue.Add(parse.AttNSN(att))
}

func (ctlr *IPAMController) OnAttachmentUpdate(oldObj, newObj interface{}) {
	oldAtt := oldObj.(*netv1a1.NetworkAttachment)
	newAtt := newObj.(*netv1a1.NetworkAttachment)
	glog.V(5).Infof("Notified of update of NetworkAttachment from %#+v to %#+v\n", oldAtt, newAtt)
	ctlr.queue.Add(parse.AttNSN(newAtt))
}

func (ctlr *IPAMController) OnAttachmentDelete(obj interface{}) {
	att := parse.Peel(obj).(*netv1a1.NetworkAttachment)
	glog.V(5).Infof("Notified of deletion of NetworkAttachment %#+v\n", att)
	ctlr.queue.Add(parse.AttNSN(att))
}

func (ctlr *IPAMController) OnLockCreate(obj interface{}) {
	ipl := obj.(*netv1a1.IPLock)
	ctlr.OnLockNotify(ipl, "create", true)
}

func (ctlr *IPAMController) OnLockUpdate(old, new interface{}) {
	newIPL := new.(*netv1a1.IPLock)
	ctlr.OnLockNotify(newIPL, "update", true)
}

func (ctlr *IPAMController) OnLockDelete(obj interface{}) {
	ipl := obj.(*netv1a1.IPLock)
	ctlr.OnLockNotify(ipl, "delete", false)
}

func (ctlr *IPAMController) OnLockNotify(ipl *netv1a1.IPLock, op string, exists bool) {
	glog.V(4).Infof("Notified of %s of IPLock %s/%s=%s\n", op, ipl.Namespace, ipl.Name, string(ipl.UID))
	vni, addrU, err := parseIPLockName(ipl.Name)
	if err != nil {
		glog.Errorf("Error parsing IPLock name %q: %s\n", ipl.Name, err.Error())
		return
	}
	var changed bool
	var addrOp string
	if exists {
		addrOp = "ensured"
		changed = ctlr.TakeAddress(vni, addrU)
	} else {
		addrOp = "released"
		changed = ctlr.ReleaseAddress(vni, addrU)
	}
	ownerNames, _ := OwningAttachments(ipl)
	glog.V(4).Infof("At notify of %s of IPLock %s/%s, %s %s, changed=%v, numOwners=%d\n", op, ipl.Namespace, ipl.Name, addrOp, convert.Uint32ToIPv4(addrU), changed, len(ownerNames))
	for _, ownerName := range ownerNames {
		glog.V(5).Infof("Queuing NetworkAttachment %s/%s due to notification about IPLock %s\n", ipl.Namespace, ownerName, ipl.Name)
		ctlr.queue.Add(k8stypes.NamespacedName{ipl.Namespace, ownerName})
	}
}

func (ctlr *IPAMController) TakeAddress(vni, addrU uint32) (changed bool) {
	ctlr.addrCacheMutex.Lock()
	defer func() { ctlr.addrCacheMutex.Unlock() }()
	addrs := ctlr.addrCache[vni]
	if addrs == nil {
		addrs = uint32set.NewSortedUInt32Set(1)
		ctlr.addrCache[vni] = addrs
	}
	return addrs.Add(addrU)
}

func (ctlr *IPAMController) PickAddress(vni, min, max uint32) (addrU uint32, ok bool) {
	ctlr.addrCacheMutex.Lock()
	defer func() { ctlr.addrCacheMutex.Unlock() }()
	addrs := ctlr.addrCache[vni]
	if addrs == nil {
		addrs = uint32set.NewSortedUInt32Set(1)
		ctlr.addrCache[vni] = addrs
	}
	return addrs.AddOneInRange(min, max)
}

func (ctlr *IPAMController) ReleaseAddress(vni, addrU uint32) (changed bool) {
	ctlr.addrCacheMutex.Lock()
	defer func() { ctlr.addrCacheMutex.Unlock() }()
	addrs := ctlr.addrCache[vni]
	if addrs == nil {
		return
	}
	changed = addrs.Remove(addrU)
	if addrs.IsEmpty() {
		delete(ctlr.addrCache, vni)
	}
	return
}

func (ctlr *IPAMController) processQueue() {
	for {
		item, stop := ctlr.queue.Get()
		if stop {
			return
		}
		nsn := item.(k8stypes.NamespacedName)
		ctlr.processQueueItem(nsn)
	}
}

func (ctlr *IPAMController) processQueueItem(nsn k8stypes.NamespacedName) {
	defer ctlr.queue.Done(nsn)
	err := ctlr.processNetworkAttachment(nsn.Namespace, nsn.Name)
	requeues := ctlr.queue.NumRequeues(nsn)
	if err == nil {
		glog.V(4).Infof("Finished %s with %d requeues\n", nsn, requeues)
		ctlr.queue.Forget(nsn)
		return
	}
	glog.Warningf("Failed processing %s, requeuing (%d earlier requeues): %s\n", nsn, requeues, err.Error())
	ctlr.queue.AddRateLimited(nsn)
}

func (ctlr *IPAMController) processNetworkAttachment(ns, name string) error {
	att, err := ctlr.netattLister.NetworkAttachments(ns).Get(name)
	if err != nil && !k8serrors.IsNotFound(err) {
		// This should never happen.  No point in retrying.
		glog.Errorf("NetworkAttachment Lister failed to lookup %s/%s: %s\n",
			ns, name, err.Error())
		return nil
	}
	nadat := ctlr.getNetworkAttachmentData(ns, name, att != nil)
	subnetName, subnetRV, desiredVNI, desiredBaseU, desiredLastU, lockInStatus, lockForStatus, naStatusErrs, err, ok := ctlr.analyzeAndRelease(ns, name, att, nadat)
	if err != nil || !ok {
		return err
	}
	if att == nil {
		if nadat != nil {
			ctlr.clearNetworkAttachmentData(ns, name)
		}
		return nil
	}
	if lockInStatus.Obj != nil {
		return nil
	}
	var ipForStatus gonet.IP
	anticipationUsed := false
	withClue := false
	defer func() {
		if anticipationUsed {
			if withClue {
				ctlr.anticipationUsedHistogram.Observe(1)
			} else {
				ctlr.anticipationUsedHistogram.Observe(2)
			}
			glog.V(5).Infof("Anticipation used withClue=%v for attachment=%s/%s, resourceVersion=%s\n", withClue, ns, name, att.ResourceVersion)
		} else {
			ctlr.anticipationUsedHistogram.Observe(0)
		}
	}()
	var fullSubnetErr error
	if len(naStatusErrs) > 0 {
	} else if lockForStatus.Obj != nil {
		ipForStatus = lockForStatus.GetIP()
		if ipForStatus.Equal(nadat.anticipatedIPv4) {
			anticipationUsed = true
			withClue = true
			return nil
		}
	} else if nadat.anticipatedIPv4 != nil {
		anticipationUsed = true
		return nil
	} else {
		lockForStatus, ipForStatus, err = ctlr.pickAndLockAddress(ns, name, att, subnetName, desiredVNI, desiredBaseU, desiredLastU)
		if isFullSubnetErr(err) && !fullSubnetMsgFound(att.Status.Errors.IPAM) {
			fullSubnetErr = err
			naStatusErrs = []string{fullSubnetStatusMsg}
		} else if err != nil {
			return err
		}
	}
	err = ctlr.updateNAStatus(ns, name, att, nadat, naStatusErrs, subnetRV, lockForStatus, ipForStatus)
	if fullSubnetErr != nil {
		if err != nil {
			return fmt.Errorf("%s; %s", fullSubnetErr.Error(), err.Error())
		}
		return fullSubnetErr
	}
	return err
}

func isFullSubnetErr(e error) bool {
	return e != nil && strings.Contains(e.Error(), fullSubnetErrMsgPrefix)
}

func fullSubnetMsgFound(messages []string) (found bool) {
	for _, m := range messages {
		if found = fullSubnetStatusMsg == m; found {
			return
		}
	}
	return
}

func (ctlr *IPAMController) analyzeAndRelease(ns, name string, att *netv1a1.NetworkAttachment, nadat *NetworkAttachmentData) (subnetName, subnetRV string, desiredVNI, desiredBaseU, desiredLastU uint32, lockInStatus, lockForStatus ParsedLock, statusErrs []string, err error, ok bool) {
	statusLockUID := "<none>"
	ipInStatus := ""
	attUID := "."
	attRV := "."
	subnetRV = "."
	var subnet *netv1a1.Subnet
	if att != nil {
		statusLockUID = att.Status.LockUID
		subnetName = att.Spec.Subnet
		ipInStatus = att.Status.IPv4
		attRV = att.ResourceVersion
		attUID = string(att.UID)
		subnet, err = ctlr.subnetLister.Subnets(ns).Get(subnetName)
		if err != nil && !k8serrors.IsNotFound(err) {
			glog.Errorf("Subnet Lister failed to lookup %s, referenced from attachment %s/%s: %s\n", subnetName, ns, name, err.Error())
			err = nil
			return
		}
		if subnet != nil && subnet.Status.Validated {
			desiredVNI = subnet.Spec.VNI
			subnetRV = subnet.ResourceVersion
			var ipNet *gonet.IPNet
			_, ipNet, err = gonet.ParseCIDR(subnet.Spec.IPv4)
			if err != nil {
				glog.Warningf("NetworkAttachment %s/%s references subnet %s, which has malformed Spec.IPv4 %q: %s\n", ns, name, subnetName, subnet.Spec.IPv4, err.Error())
				// Subnet update should trigger reconsideration of this attachment
				statusErrs = []string{fmt.Sprintf("Subnet %s has malformed IPv4 CIDR block (%s)", subnetName, subnet.Spec.IPv4)}
				err = nil
				ok = true
				return
			}
			desiredBaseU, desiredLastU = convert.IPNetToBoundsU(ipNet)
		} else {
			if subnet == nil {
				glog.Errorf("NetworkAttachment %s/%s references Subnet %s, which does not exist now", ns, name, subnetName)
				// This attachment will be requeued upon notification of subnet creation
				statusErrs = []string{fmt.Sprintf("Subnet %s does not exist", subnetName)}
			} else {
				glog.Errorf("NetworkAttachment %s/%s references Subnet %s, which has not passed validation", ns, name, subnetName)
				// If the subnet passes validation in the future the attachment
				// will be requeued upon notification of subnet validation
				statusErrs = []string{fmt.Sprintf("Subnet %s has not passed validation", subnetName)}
			}
			err = nil
			ok = true
			return
		}
	}
	var ownedObjs []interface{}
	iplIndexer := ctlr.lockInformer.GetIndexer()
	ownedObjs, err = iplIndexer.ByIndex(owningAttachmentIdxName, name)
	if err != nil {
		glog.Errorf("iplIndexer.ByIndex(%s, %s) failed: %s\n", owningAttachmentIdxName, name, err.Error())
		// Retry unlikely to help
		err = nil
		return
	}
	var timeSlippers, undesiredLocks, usableLocks ParsedLockList
	considered := make(map[uint32]struct{})
	consider := func(ipl *netv1a1.IPLock) {
		parsed, parseErr := NewParsedLock(ipl)
		if parseErr != nil {
			return
		}
		considered[parsed.addrU] = struct{}{}
		_, ownerUID := GetOwner(ipl, "NetworkAttachment")
		if att != nil && ownerUID != att.UID {
			// This is for an older or newer edition of `att`; ignore it.
			// The garbage collector will get it if need be.
			// That may take a while, but that is better than deleting a lock
			// owned by a more recent edition of `att`.
			timeSlippers = timeSlippers.Append(parsed)
			return
		}
		if parsed.VNI != desiredVNI || parsed.addrU < desiredBaseU || parsed.addrU > desiredLastU {
			undesiredLocks = undesiredLocks.Append(parsed)
			return
		}
		if string(parsed.UID) == statusLockUID && att != nil && att.Status.IPv4 != "" && att.Status.IPv4 == parsed.GetIP().String() {
			lockInStatus = parsed
		}
		usableLocks = usableLocks.Append(parsed)
	}
	for _, ownedObj := range ownedObjs {
		ipl := ownedObj.(*netv1a1.IPLock)
		consider(ipl)
	}
	if att != nil && att.Status.IPv4 != "" {
		// Make sure we do not skip this one just because we have not
		// yet been notified about it.
		statusIP := gonet.ParseIP(att.Status.IPv4)
		if statusIP != nil {
			statusIPU := convert.IPv4ToUint32(statusIP)
			statusUsed := float64(0)
			defer func() { ctlr.statusUsedHistogram.Observe(statusUsed) }()
			if _, found := considered[statusIPU]; !found {
				antName := makeIPLockName2(desiredVNI, statusIP)
				ipl, err := ctlr.netIfc.IPLocks(ns).Get(antName, k8smetav1.GetOptions{})
				if err != nil {
					glog.Infof("For NetworkAttachment %s/%s failed to fetch lock %s for IP in Status: %s\n", ns, name, antName, err.Error())
				} else {
					on, _ := GetOwner(ipl, "NetworkAttachment")
					if on == name {
						statusUsed = 1
						consider(ipl)
					}
				}
			}
		}
	}
	if nadat != nil && (att == nil || nadat.anticipatingResourceVersion != att.ResourceVersion && nadat.anticipatedResourceVersion != att.ResourceVersion || nadat.anticipationSubnetRV != subnetRV) {
		nadat.anticipatingResourceVersion = ""
		nadat.anticipatedResourceVersion = ""
		nadat.anticipationSubnetRV = ""
		nadat.anticipatedIPv4 = nil
	}
	var usableToRelease ParsedLockList
	if att == nil {
		usableToRelease = usableLocks
	} else if lockInStatus.Obj != nil {
		usableToRelease, _ = usableLocks.RemFunc(lockInStatus)
	} else if len(usableLocks) > 0 {
		// Make a deterministic choice, so that if there are multiple
		// controllers they have a fighting chance of making the same decision.
		// Pick the oldest for stability's sake.
		lockForStatus = usableLocks.Best()
		usableToRelease, _ = usableLocks.RemFunc(lockForStatus)
	}
	locksToRelease, _ := undesiredLocks.AddListFunc(usableToRelease)
	anticipatedIPStr := "."
	if nadat != nil && nadat.anticipatedIPv4 != nil {
		anticipatedIPStr = nadat.anticipatedIPv4.String()
	}
	glog.V(4).Infof("processNetworkAttachment analyzed; na=%s/%s=%s, naRV=%s, subnet=%s, shouldExist=%v, desiredVNI=%x, desiredBaseU=%x, desiredLastU=%x, lockInStatus=%s, lockForStatus=%s, locksToRelease=%s, timeSlippers=%s, Status.IPv4=%q, anticipatedIP=%s", ns, name, attUID, attRV, subnetName, att != nil, desiredVNI, desiredBaseU, desiredLastU, lockInStatus, lockForStatus, locksToRelease, timeSlippers, ipInStatus, anticipatedIPStr)
	for _, lockToRelease := range locksToRelease {
		err = ctlr.deleteIPLockObject(lockToRelease)
		if err != nil {
			return
		}
	}
	ok = true
	return
}

func (ctlr *IPAMController) deleteIPLockObject(parsed ParsedLock) error {
	lockOps := ctlr.netIfc.IPLocks(parsed.ns)
	delOpts := k8smetav1.DeleteOptions{
		Preconditions: &k8smetav1.Preconditions{UID: &parsed.UID},
	}
	tBefore := time.Now()
	err := lockOps.Delete(parsed.name, &delOpts)
	tAfter := time.Now()
	ctlr.lockOpHistograms.With(prometheus.Labels{"op": "delete", "err": FormatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
	if err == nil {
		glog.V(4).Infof("Deleted IPLock %s/%s=%s\n", parsed.ns, parsed.name, string(parsed.UID))
	} else if k8serrors.IsNotFound(err) || k8serrors.IsGone(err) {
		glog.V(4).Infof("IPLock %s/%s=%s is undesired and already gone\n", parsed.ns, parsed.name, string(parsed.UID))
	} else {
		return err
	}
	return nil
}

func (ctlr *IPAMController) pickAndLockAddress(ns, name string, att *netv1a1.NetworkAttachment, subnetName string, vni, subnetBaseU, subnetLastU uint32) (lockForStatus ParsedLock, ipForStatus gonet.IP, err error) {
	addrMin, addrMax := subnetBaseU, subnetLastU
	if addrMax-addrMin >= 4 {
		addrMin, addrMax = subnetBaseU+2, subnetLastU-1
	}
	ipForStatusU, ok := ctlr.PickAddress(vni, addrMin, addrMax)
	if !ok {
		err = fmt.Errorf("%s %s/%s (%x/%x--%x)", fullSubnetErrMsgPrefix, ns, subnetName, vni, subnetBaseU, subnetLastU)
		return
	}
	ipForStatus = convert.Uint32ToIPv4(ipForStatusU)
	glog.V(4).Infof("Picked address %s from %x/%x--%x for %s/%s\n", ipForStatus, vni, subnetBaseU, subnetLastU, ns, name)

	// Now, try to lock that address

	lockName := makeIPLockName2(vni, ipForStatus)
	lockForStatus = ParsedLock{ns, lockName, vni, ipForStatusU, k8stypes.UID(""), time.Time{}, nil}
	aTrue := true
	owners := []k8smetav1.OwnerReference{{
		APIVersion: netv1a1.SchemeGroupVersion.String(),
		Kind:       "NetworkAttachment",
		Name:       name,
		UID:        att.UID,
		Controller: &aTrue,
	}}
	ipl := &netv1a1.IPLock{
		ObjectMeta: k8smetav1.ObjectMeta{
			Namespace:       ns,
			Name:            lockName,
			OwnerReferences: owners,
		},
		Spec: netv1a1.IPLockSpec{SubnetName: subnetName},
	}
	lockOps := ctlr.netIfc.IPLocks(ns)
	var ipl2 *netv1a1.IPLock
	for {
		tBefore := time.Now()
		ipl2, err = lockOps.Create(ipl)
		tAfter := time.Now()
		ctlr.lockOpHistograms.With(prometheus.Labels{"op": "create", "err": FormatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
		if err == nil {
			ctlr.eventRecorder.Eventf(att, k8scorev1api.EventTypeNormal, "AddressAssigned", "Assigned IPv4 address %s", ipForStatus)
			glog.V(4).Infof("Locked IP address %s for %s/%s=%s, lockName=%s, lockUID=%s\n", ipForStatus, ns, name, string(att.UID), lockName, string(ipl2.UID))
			ctlr.attachmentCreateToLockHistogram.Observe(ipl2.CreationTimestamp.Sub(att.CreationTimestamp.Time).Seconds())
			break
		} else if k8serrors.IsAlreadyExists(err) {
			// Maybe it is ours
			var err2 error
			ipl2, err2 = lockOps.Get(lockName, k8smetav1.GetOptions{})
			var ownerName string
			var ownerUID k8stypes.UID
			if err2 == nil {
				ownerName, ownerUID = GetOwner(ipl2, "NetworkAttachment")
			} else if k8serrors.IsNotFound(err2) {
				// It was just there, now it is gone; try again to create
				glog.Warningf("IPLock %s disappeared before our eyes\n", lockName)
				continue
			} else {
				err = fmt.Errorf("failed to fetch allegedly existing IPLock %s for %s/%s: %s", lockName, ns, name, err2.Error())
				return
			}
			if ownerName == name && ownerUID == att.UID {
				// Yes, it's ours!
				glog.V(4).Infof("Recovered lockName=%s, lockUID=%s on address %s for %s/%s=%s\n", lockName, string(ipl2.UID), ipForStatus, ns, name, string(att.UID))
				err = nil
				break
			} else {
				glog.V(4).Infof("Collision at IPLock %s for %s/%s=%s, owner is %s=%s\n", lockName, ns, name, string(att.UID), ownerName, string(ownerUID))
				// The cache in snd failed to avoid this collision.
				// Leave the bit set it the cache, something else is holding it.
				// Retry in a while
				err = fmt.Errorf("cache incoherence at %s", lockName)
				return
			}
		}
		releaseOK := ctlr.ReleaseAddress(vni, ipForStatusU)
		if k8serrors.IsInvalid(err) || strings.Contains(strings.ToLower(err.Error()), "invalid") {
			glog.Errorf("Permanent error creating IPLock %s for %s/%s (releaseOK=%v): %s\n", lockName, ns, name, releaseOK, err.Error())
			err = nil
		} else {
			glog.Warningf("Transient error creating IPLock %s for %s/%s (releaseOK=%v): %s\n", lockName, ns, name, releaseOK, err.Error())
			err = fmt.Errorf("Create of IPLock %s for %s/%s failed: %s", lockName, ns, name, err.Error())
		}
		return
	}
	lockForStatus.UID = ipl2.UID
	lockForStatus.CreationTime = ipl2.CreationTimestamp.Time
	lockForStatus.Obj = ipl2
	return
}

func (ctlr *IPAMController) updateNAStatus(ns, name string, att *netv1a1.NetworkAttachment, nadat *NetworkAttachmentData, statusErrs []string, subnetRV string, lockForStatus ParsedLock, ipForStatus gonet.IP) error {
	att2 := att.DeepCopy()
	att2.Status.Errors.IPAM = statusErrs
	att2.Status.LockUID = string(lockForStatus.UID)
	att2.Status.AddressVNI = lockForStatus.VNI
	if ipForStatus == nil {
		att2.Status.IPv4 = ""
	} else {
		att2.Status.IPv4 = ipForStatus.String()
	}
	attachmentOps := ctlr.netIfc.NetworkAttachments(ns)
	tBefore := time.Now()
	att3, err := attachmentOps.Update(att2)
	tAfter := time.Now()
	ctlr.attachmentUpdateHistograms.With(prometheus.Labels{"statusErr": FormatErrVal(len(statusErrs) > 0), "err": FormatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
	if err == nil {
		t1 := att.CreationTimestamp.Time
		t2 := tAfter.Truncate(time.Second)
		deltaS := t2.Sub(t1).Seconds()
		ctlr.attachmentCreateToAddressHistogram.Observe(deltaS)
		if len(statusErrs) > 0 {
			glog.V(4).Infof("Recorded errors %v in status of %s/%s, old ResourceVersion=%s, new ResourceVersion=%s\n", statusErrs, ns, name, att.ResourceVersion, att3.ResourceVersion)
		} else {
			glog.V(4).Infof("Recorded locked address %s in status of %s/%s, old ResourceVersion=%s, new ResourceVersion=%s, subnetRV=%s\n", ipForStatus, ns, name, att.ResourceVersion, att3.ResourceVersion, subnetRV)
			nadat.anticipatingResourceVersion = att.ResourceVersion
			nadat.anticipatedResourceVersion = att3.ResourceVersion
			nadat.anticipationSubnetRV = subnetRV
			nadat.anticipatedIPv4 = ipForStatus
		}
		return nil
	}
	errMsg := fmt.Sprintf("Failed to write into status of NetworkAttachment %s/%s", ns, name)
	if len(statusErrs) > 0 {
		errMsg = fmt.Sprintf("%s error messages %v", errMsg, statusErrs)
	} else {
		errMsg = fmt.Sprintf("%s allocated address %s", errMsg, ipForStatus)
	}
	if k8serrors.IsNotFound(err) {
		glog.V(4).Infof("%s: NetworkAttachment was deleted.", errMsg)
		return nil
	}
	return fmt.Errorf("%s: %s", errMsg, err.Error())
}

func (ctlr *IPAMController) getNetworkAttachmentData(ns, name string, addIfMissing bool) *NetworkAttachmentData {
	added := false
	ctlr.attsMutex.Lock()
	defer func() {
		ctlr.attsMutex.Unlock()
		if added {
			glog.V(4).Infof("Created NetworkAttachmentData for %s/%s\n", ns, name)
		}
	}()
	nadata := ctlr.atts[k8stypes.NamespacedName{ns, name}]
	if nadata == nil {
		if !addIfMissing {
			return nil
		}
		nadata = &NetworkAttachmentData{}
		ctlr.atts[k8stypes.NamespacedName{ns, name}] = nadata
		added = true
	}
	return nadata
}

func (ctlr *IPAMController) clearNetworkAttachmentData(ns, name string) {
	had := false
	ctlr.attsMutex.Lock()
	defer func() {
		ctlr.attsMutex.Unlock()
		if had {
			glog.V(4).Infof("Deleted NetworkAttachmentData for %s/%s\n", ns, name)
		}
	}()
	_, had = ctlr.atts[k8stypes.NamespacedName{ns, name}]
	if had {
		delete(ctlr.atts, k8stypes.NamespacedName{ns, name})
	}
}

func AttachmentSubnets(obj interface{}) (subnets []string, err error) {
	att := obj.(*netv1a1.NetworkAttachment)
	return []string{att.Spec.Subnet}, nil
}

var _ k8scache.IndexFunc = AttachmentSubnets

func OwningAttachments(obj interface{}) (owners []string, err error) {
	meta := obj.(k8smetav1.Object)
	owners = make([]string, 0, 1)
	for _, oref := range meta.GetOwnerReferences() {
		if oref.Kind == "NetworkAttachment" && oref.Controller != nil && *oref.Controller {
			owners = append(owners, oref.Name)
		}
	}
	return
}

var _ k8scache.IndexFunc = OwningAttachments

func GetOwner(obj k8smetav1.Object, ownerKind string) (name string, uid k8stypes.UID) {
	for _, oref := range obj.GetOwnerReferences() {
		if oref.Kind == ownerKind && oref.Controller != nil && *oref.Controller {
			name = oref.Name
			uid = oref.UID
		}
	}
	return
}

func makeIPLockName2(VNI uint32, ip gonet.IP) string {
	ipv4 := ip.To4()
	return fmt.Sprintf("v1-%d-%d-%d-%d-%d", VNI, ipv4[0], ipv4[1], ipv4[2], ipv4[3])
}

func parseIPLockName(lockName string) (VNI uint32, addrU uint32, err error) {
	parts := strings.Split(lockName, "-")
	if len(parts) != 6 || parts[0] != "v1" {
		return 0, 0, fmt.Errorf("Lock name %q is malformed", lockName)
	}
	vni64, err2 := strconv.ParseUint(parts[1], 10, 21)
	if err2 != nil {
		return 0, 0, fmt.Errorf("VNI in lockName %q is malformed: %s", lockName, err2)
	}
	VNI = uint32(vni64)
	for i := 0; i < 4; i++ {
		b64, err := strconv.ParseUint(parts[2+i], 10, 8)
		if err != nil {
			return 0, 0, fmt.Errorf("lockName %q is malformed at address byte %d: %s", lockName, i, err.Error())
		}
		addrU = addrU*256 + uint32(b64)
	}
	return
}

// ParsedLock characterizes an IPLock object and
// optionally including a pointer to the object.
type ParsedLock struct {
	ns   string
	name string

	VNI uint32

	// addrU is the locked address, expressed as a number.
	addrU uint32

	// UID identifies the lock object
	UID k8stypes.UID

	// CreationTime characterizes the lock object
	CreationTime time.Time
	Obj          *netv1a1.IPLock
}

func NewParsedLock(ipl *netv1a1.IPLock) (ans ParsedLock, err error) {
	vni, addrU, err := parseIPLockName(ipl.Name)
	if err == nil {
		ans = ParsedLock{ipl.Namespace, ipl.Name, vni, addrU, ipl.UID, ipl.CreationTimestamp.Time, ipl}
	}
	return
}

var _ fmt.Stringer = ParsedLock{}

func (x ParsedLock) String() string {
	return fmt.Sprintf("%d/%x=%s@%s", x.VNI, x.addrU, string(x.UID), x.CreationTime)
}

func (x ParsedLock) GetIP() gonet.IP {
	return convert.Uint32ToIPv4(x.addrU)
}

func (x ParsedLock) Equal(y ParsedLock) bool {
	return x.VNI == y.VNI && x.UID == y.UID &&
		x.CreationTime == y.CreationTime && x.addrU == y.addrU
}

func (x ParsedLock) IsBetterThan(y ParsedLock) bool {
	if x.CreationTime != y.CreationTime {
		return x.CreationTime.Before(y.CreationTime)
	}
	return strings.Compare(string(x.UID), string(y.UID)) > 0
}

type ParsedLockList []ParsedLock

func (list ParsedLockList) Best() ParsedLock {
	if len(list) == 0 {
		return ParsedLock{}
	}
	ans := list[0]
	for _, elt := range list[1:] {
		if elt.IsBetterThan(ans) {
			ans = elt
		}
	}
	return ans
}

func (list ParsedLockList) Append(elt ...ParsedLock) ParsedLockList {
	return ParsedLockList(append(list, elt...))
}

func (list ParsedLockList) AddFunc(elt ParsedLock) (with ParsedLockList, diff bool) {
	if len(list) == 0 {
		return []ParsedLock{elt}, true
	}
	for _, x := range list {
		if x.Equal(elt) {
			return list, false
		}
	}
	with = make([]ParsedLock, 0, 1+len(list))
	with = append(with, list...)
	with = append(with, elt)
	return with, true
}

func (list ParsedLockList) AddListFunc(list2 ParsedLockList) (with ParsedLockList, diff bool) {
	with, diff = list, false
	for _, elt := range list2 {
		var diffHere bool
		with, diffHere = with.AddFunc(elt)
		diff = diff || diffHere
	}
	return
}

func (list ParsedLockList) RemFunc(elt ParsedLock) (sans ParsedLockList, diff bool) {
	if len(list) == 0 {
		return nil, false
	}
	l := len(list)
	if l == 1 {
		if elt.Equal(list[0]) {
			return nil, true
		}
		return list, false
	}
	for i, x := range list {
		if x.Equal(elt) {
			sans = make([]ParsedLock, 0, len(list)-1)
			sans = append(sans, list[0:i]...)
			if i+1 < l {
				sans = append(sans, list[i+1:]...)
			}
			return sans, true
		}
	}
	return list, false
}

func FormatErrVal(err bool) string {
	if err {
		return "err"
	}
	return "ok"
}
