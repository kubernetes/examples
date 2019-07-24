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

package subnet

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

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
	kosscheme "k8s.io/examples/staging/kos/pkg/client/clientset/versioned/scheme"
	kosclientv1a1 "k8s.io/examples/staging/kos/pkg/client/clientset/versioned/typed/network/v1alpha1"
	netlistv1a1 "k8s.io/examples/staging/kos/pkg/client/listers/network/v1alpha1"
	"k8s.io/examples/staging/kos/pkg/util/parse"
	"k8s.io/examples/staging/kos/pkg/util/parse/network/subnet"
)

const (
	subnetVNIField = "spec.vni"

	// label values for metrics tracking mismatches between conflicts caches and
	// live lists results.
	missingCacheMismatch = "missing cache"
	cacheOwnerMismatch   = "cache owner"

	metricsNamespace = "kos"
	metricsSubsystem = "subnet_validator"
)

// conflictsCache holds information for one subnet regarding conflicts with
// other subnets. There's no guarantee that the cache is up-to-date. For
// instance, if a conflicting  subnet X is in it and gets deleted it is not
// removed.
type conflictsCache struct {
	// ownerUID is the UID of the subnet owning the conflicts cache.
	ownerUID k8stypes.UID

	// rivals approximately identifies the subnets that have a conflict with the
	// subnet owning the cache. If subnet X is the owner of the conflicts cache
	// and a namespaced name Y is in rivals this means that in the past a subnet
	// with namespaced name Y was processed by a queue worker and a conflict
	// with X was found.
	rivals []k8stypes.NamespacedName
}

// Validator validates subnets and writes the outcome in their status.
// Validation consists of two checks:
//
// 		(1) CIDRs for subnets with the same VNI are disjoint.
// 		(2) all subnets with the same VNI are within the same K8s namespace.
//
// If a subnet S1 does not pass validation because of a conflict with another
// subnet S2, upon deletion of S2 S1 is validated again. Validator uses an
// informer to watch for subnets, but does a live list against the API server to
// retrieve the conflicting subnets when validating a subnet. This avoids race
// conditions caused by multiple validators running at the same time.
type Validator struct {
	netIfc         kosclientv1a1.NetworkV1alpha1Interface
	subnetInformer k8scache.SharedInformer
	subnetLister   netlistv1a1.SubnetLister
	eventRecorder  k8seventrecord.EventRecorder
	queue          k8sworkqueue.RateLimitingInterface
	workers        int

	// conflicts associates a subnet namespaced name with its conflictsCache.
	// Always access while holding conflictsMutex.
	conflicts      map[k8stypes.NamespacedName]*conflictsCache
	conflictsMutex sync.Mutex

	// staleRVs associates a subnet X's namespaced name for which there was a
	// successful status update to X's resource version prior to the update.
	// When a worker begins processing a subnet X, it checks whether X's
	// resource version matches the resource version in staleRVs[X]. If that's
	// the case X is stale, i.e. it does not reflect the latest update yet,
	// hence processing is postponed to when the notification for the update is
	// received.
	// Only access while holding staleRVsMutex.
	staleRVs      map[k8stypes.NamespacedName]string
	staleRVsMutex sync.RWMutex

	// Latency from subnet ObjectMeta.CreationTimestamp to return from update
	// writing validation outcome in status.
	subnetCreateToValidatedHistograms *prometheus.HistogramVec

	// Round trip time to update Subnet status.
	subnetUpdateHistograms *prometheus.HistogramVec

	// Round trip time of live lists to fetch subnets.
	liveListHistograms *prometheus.HistogramVec

	// Number of subnets returned by live lists.
	liveListResultLengthHistogram prometheus.Histogram

	// Number of times a worker processed a subnet all the way to the status
	// update to find out that the status was already up to date.
	duplicateWorkCount prometheus.Counter

	// Number of times work on a subnet was suppressed because the subnet was
	// stale.
	staleSubnetsSuppressionCount prometheus.Counter

	// Number of times the subnet received from the API server did not match the
	// one owning the conflicts cache. This also counts cases where a subnet
	// is received but the conflicts cache is not there.
	cacheVsLiveSubnetMismatches *prometheus.CounterVec
}

func NewValidationController(netIfc kosclientv1a1.NetworkV1alpha1Interface,
	subnetInformer k8scache.SharedInformer,
	subnetLister netlistv1a1.SubnetLister,
	eventIfc k8scorev1client.EventInterface,
	queue k8sworkqueue.RateLimitingInterface,
	workers int,
	hostname string) *Validator {

	subnetCreateToValidatedHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "subnet_create_to_validated_latency_seconds",
			Help:      "Latency from subnet CreationTimestamp to return from update writing validation outcome in status per outcome, in seconds.",
			Buckets:   []float64{-1, 0, 1, 2, 3, 4, 6, 8, 12, 16, 24, 32, 64},
		},
		[]string{"statusErr"})
	errValT, errValF := FormatErrVal(true), FormatErrVal(false)
	subnetCreateToValidatedHistograms.With(prometheus.Labels{"statusErr": errValT})
	subnetCreateToValidatedHistograms.With(prometheus.Labels{"statusErr": errValF})

	subnetUpdateHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "subnet_update_latency_seconds",
			Help:      "Round trip time to update subnet status, in seconds.",
			Buckets:   []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64},
		},
		[]string{"err", "statusErr"})
	subnetUpdateHistograms.With(prometheus.Labels{"err": errValT, "statusErr": errValT})
	subnetUpdateHistograms.With(prometheus.Labels{"err": errValF, "statusErr": errValT})
	subnetUpdateHistograms.With(prometheus.Labels{"err": errValT, "statusErr": errValF})
	subnetUpdateHistograms.With(prometheus.Labels{"err": errValF, "statusErr": errValF})

	liveListHistograms := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "live_list_latency_seconds",
			Help:      "Round trip time of live lists to fetch subnets, in seconds.",
			Buckets:   []float64{-0.125, 0, 0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64},
		},
		[]string{"err"})
	liveListHistograms.With(prometheus.Labels{"err": errValT})
	liveListHistograms.With(prometheus.Labels{"err": errValF})

	liveListResultLengthHistogram := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "number_of_subnets_returned_by_live_list",
			Help:      "Number of subnets returned by live lists.",
			Buckets:   []float64{-1, 0, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192},
		})

	duplicateWorkCount := prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "duplicate_work_count",
			Help:      "Number of times a subnet was processed but there was no status update because the status was already up to date.",
		})

	staleSubnetsSuppressionCount := prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "stale_subnet_work_suppression_count",
			Help:      "Number of times processing of a subnet stopped because the subnet was stale.",
		})

	cacheVsLiveSubnetMismatches := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "subnet_live_vs_conflict_cache_count",
			Help:      "Number of times processing of a subnet stopped because of a mismatch between the subnet from the API server and the one associated with the conflicts cache.",
		},
		[]string{"mismatch_type"})
	cacheVsLiveSubnetMismatches.With(prometheus.Labels{"mismatch_type": missingCacheMismatch})
	cacheVsLiveSubnetMismatches.With(prometheus.Labels{"mismatch_type": cacheOwnerMismatch})

	workerCount := prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "worker_count",
			Help:      "Number of queue worker threads",
		})

	prometheus.MustRegister(subnetCreateToValidatedHistograms, subnetUpdateHistograms, liveListHistograms, liveListResultLengthHistogram, duplicateWorkCount, staleSubnetsSuppressionCount, cacheVsLiveSubnetMismatches, workerCount)

	workerCount.Add(float64(workers))

	eventBroadcaster := k8seventrecord.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.V(3).Infof)
	eventBroadcaster.StartRecordingToSink(&k8scorev1client.EventSinkImpl{eventIfc})
	eventRecorder := eventBroadcaster.NewRecorder(kosscheme.Scheme, k8scorev1api.EventSource{Component: "subnet-validator", Host: hostname})

	return &Validator{
		netIfc:                            netIfc,
		subnetInformer:                    subnetInformer,
		subnetLister:                      subnetLister,
		eventRecorder:                     eventRecorder,
		subnetCreateToValidatedHistograms: subnetCreateToValidatedHistograms,
		subnetUpdateHistograms:            subnetUpdateHistograms,
		liveListHistograms:                liveListHistograms,
		liveListResultLengthHistogram:     liveListResultLengthHistogram,
		duplicateWorkCount:                duplicateWorkCount,
		staleSubnetsSuppressionCount:      staleSubnetsSuppressionCount,
		cacheVsLiveSubnetMismatches:       cacheVsLiveSubnetMismatches,
		queue:                             queue,
		workers:                           workers,
		conflicts:                         make(map[k8stypes.NamespacedName]*conflictsCache),
		staleRVs:                          make(map[k8stypes.NamespacedName]string),
	}
}

// Run starts the validator and blocks until stop is closed. This entails
// starting its Informer and the worker goroutines.
func (v *Validator) Run(stop <-chan struct{}) error {
	defer k8sutilruntime.HandleCrash()
	defer v.queue.ShutDown()

	klog.Info("Starting subnet validation controller.")
	defer klog.Info("Shutting down subnet validation controller.")

	v.subnetInformer.AddEventHandler(v)

	if !k8scache.WaitForCacheSync(stop, v.subnetInformer.HasSynced) {
		return errors.New("informer cache failed to sync")
	}
	klog.V(2).Infof("Informer cache synced.")

	// Start workers.
	for i := 0; i < v.workers; i++ {
		go k8swait.Until(v.processQueue, time.Second, stop)
	}
	klog.V(2).Infof("Launched %d workers.", v.workers)

	<-stop

	return nil
}

func (v *Validator) OnAdd(obj interface{}) {
	s := obj.(*netv1a1.Subnet)
	klog.V(5).Infof("Notified of creation of %#+v.", s)
	v.queue.Add(k8stypes.NamespacedName{
		Namespace: s.Namespace,
		Name:      s.Name,
	})
}

func (v *Validator) OnUpdate(oldObj, newObj interface{}) {
	oldS, newS := oldObj.(*netv1a1.Subnet), newObj.(*netv1a1.Subnet)
	klog.V(5).Infof("Notified of update from %#+v to %#+v.", oldS, newS)

	nsn := k8stypes.NamespacedName{
		Namespace: newS.Namespace,
		Name:      newS.Name,
	}

	// Since only VNI and CIDR block affect validation and they're immutable
	// don't enqueue on update notifications, except for those which are caused
	// by a delete followed by a re-create (UID change) or those caused by
	// status updates from this controller so that all the processing which was
	// not performed because the subnet in the Informer's cache was stale can be
	// performed.
	if oldS.UID != newS.UID || v.subnetIsStaleNoMore(nsn, newS.ResourceVersion) {
		v.queue.Add(nsn)
	}
}

func (v *Validator) subnetIsStaleNoMore(subnet k8stypes.NamespacedName, rv string) bool {
	v.staleRVsMutex.RLock()
	defer v.staleRVsMutex.RUnlock()

	if oldRV, found := v.staleRVs[subnet]; found && oldRV != rv {
		return true
	}
	return false
}

func (v *Validator) OnDelete(obj interface{}) {
	s := parse.Peel(obj).(*netv1a1.Subnet)
	klog.V(5).Infof("Notified of deletion of %#+v.", s)
	v.queue.Add(k8stypes.NamespacedName{
		Namespace: s.Namespace,
		Name:      s.Name,
	})
}

func (v *Validator) processQueue() {
	for {
		subnet, stop := v.queue.Get()
		if stop {
			return
		}
		v.processQueueItem(subnet.(k8stypes.NamespacedName))
	}
}

func (v *Validator) processQueueItem(subnet k8stypes.NamespacedName) {
	defer v.queue.Done(subnet)
	requeues := v.queue.NumRequeues(subnet)
	if err := v.processSubnet(subnet); err != nil {
		klog.Warningf("Failed processing %s, requeuing (%d earlier requeues): %s.", subnet, requeues, err.Error())
		v.queue.AddRateLimited(subnet)
		return
	}
	klog.V(4).Infof("Finished %s with %d requeues.", subnet, requeues)
	v.queue.Forget(subnet)
}

func (v *Validator) processSubnet(subnetNSN k8stypes.NamespacedName) error {
	subnet, err := v.subnetLister.Subnets(subnetNSN.Namespace).Get(subnetNSN.Name)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			v.processDeletedSubnet(subnetNSN)
		} else {
			// This should never happen. No point in retrying.
			klog.Errorf("Subnet lister failed to lookup %s: %s.", subnetNSN, err.Error())
		}
		return nil
	}

	return v.processExistingSubnet(subnet)
}

func (v *Validator) processDeletedSubnet(s k8stypes.NamespacedName) {
	v.clearStaleRV(s)

	// Clear local state associated with s and enqueue old rivals so that they
	// can be re-validated: they might no longer have conflicts as this subnet
	// has been deleted.
	rivals := v.clearConflictsCache(s)
	for _, r := range rivals {
		v.queue.Add(r)
	}
}

func (v *Validator) processExistingSubnet(s *netv1a1.Subnet) error {
	ss, parsingErrs := subnet.NewSummary(s)
	if len(parsingErrs) > 0 {
		klog.Errorf("Subnet %s/%s is malformed: %s. This should never happen.", s.Namespace, s.Name, parsingErrs.Error())
		return nil
	}

	if v.subnetIsStale(ss.NamespacedName, s.ResourceVersion) {
		klog.V(5).Infof("Stopping processing of %s because it's stale. Processing will be restarted upon receiving the fresh version.", ss.NamespacedName)
		v.staleSubnetsSuppressionCount.Inc()
		return nil
	}

	// Clear an old conflicts cache which belonged to a deleted subnet with the
	// same namespaced name as s if it's there, then initialize s's conflicts
	// cache if this is the first time s is being processed. If an old conflicts
	// cache is found, return the rival subnets in it and re-enqueue them: their
	// conflicts might have disappeared.
	oldRivals := v.updateConflictsCache(ss.NamespacedName, ss.UID)
	for _, r := range oldRivals {
		v.queue.Add(r)
	}

	// Keep the promise that a Subnet stays validated once it becomes validated.
	if s.Status.Validated {
		return nil
	}

	// Retrieve all the subnets with the same VNI as s. Doing a live list as
	// opposed to a cache-based one (through the informer) prevents race
	// conditions that can arise in case of multiple validators running.
	tBefore := time.Now()
	potentialRivals, err := v.netIfc.Subnets(k8scorev1api.NamespaceAll).List(k8smetav1.ListOptions{
		FieldSelector: k8sfields.OneTermEqualSelector(subnetVNIField, strconv.FormatUint(uint64(ss.VNI), 10)).String(),
	})
	tAfter := time.Now()
	v.liveListHistograms.With(prometheus.Labels{"err": FormatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
	v.liveListResultLengthHistogram.Observe(float64(len(potentialRivals.Items)))
	if err != nil {
		if malformedRequest(err) {
			klog.Errorf("live list of all subnets against API server failed while validating %s: %s. There will be no retry because of the nature of the error", ss.NamespacedName, err.Error())
			// This should never happen, no point in retrying.
			return nil
		}
		return fmt.Errorf("live list of all subnets against API server failed: %s", err.Error())
	}

	// Look for conflicts with all the other subnets with the same VNI and
	// record them in the rivals conflicts caches.
	conflictsMsgs, err := v.recordConflicts(ss, potentialRivals.Items)
	if err != nil {
		return err
	}

	if err := v.updateSubnetValidity(s, conflictsMsgs); err != nil {
		return fmt.Errorf("failed to write validation outcome into status: %s", err.Error())
	}

	return nil
}

func (v *Validator) clearStaleRV(s k8stypes.NamespacedName) {
	v.staleRVsMutex.Lock()
	defer v.staleRVsMutex.Unlock()

	delete(v.staleRVs, s)
}

func (v *Validator) subnetIsStale(s k8stypes.NamespacedName, rv string) bool {
	v.staleRVsMutex.Lock()
	defer v.staleRVsMutex.Unlock()

	if v.staleRVs[s] == rv {
		return true
	}

	delete(v.staleRVs, s)
	return false
}

func (v *Validator) clearConflictsCache(s k8stypes.NamespacedName) []k8stypes.NamespacedName {
	v.conflictsMutex.Lock()
	defer v.conflictsMutex.Unlock()

	if c := v.conflicts[s]; c != nil {
		delete(v.conflicts, s)
		return c.rivals
	}

	return nil
}

func (v *Validator) updateConflictsCache(ownerNSN k8stypes.NamespacedName, ownerUID k8stypes.UID) []k8stypes.NamespacedName {
	v.conflictsMutex.Lock()
	defer v.conflictsMutex.Unlock()

	c := v.conflicts[ownerNSN]
	if c == nil {
		c = &conflictsCache{
			ownerUID: ownerUID,
			rivals:   make([]k8stypes.NamespacedName, 0),
		}
		v.conflicts[ownerNSN] = c
		return nil
	}

	if c.ownerUID != ownerUID {
		// The conflicts cache belonged to a deleted subnet with namespaced name
		// ownerNSN. Update the owner UID to reflect the fact that the conflicts
		// cache has a new owner and return all rivals so that they can be
		// re-validated again as the conflicts might have disappeared.
		c.ownerUID = ownerUID
		oldRivals := c.rivals
		c.rivals = make([]k8stypes.NamespacedName, 0)
		return oldRivals
	}

	return nil
}

func malformedRequest(e error) bool {
	return k8serrors.IsUnauthorized(e) ||
		k8serrors.IsBadRequest(e) ||
		k8serrors.IsForbidden(e) ||
		k8serrors.IsNotAcceptable(e) ||
		k8serrors.IsUnsupportedMediaType(e) ||
		k8serrors.IsMethodNotSupported(e) ||
		k8serrors.IsInvalid(e)
}

func (v *Validator) recordConflicts(candidate *subnet.Summary, potentialRivals []netv1a1.Subnet) (conflictsMsgs []string, err error) {
	for _, pr := range potentialRivals {
		potentialRival, parsingErrs := subnet.NewSummary(&pr)
		if len(parsingErrs) > 0 {
			klog.Errorf("Found malformed subnet %s/%s while validating %s: %s. This should never happen.", pr.Namespace, pr.Name, candidate.NamespacedName, parsingErrs.Error())
			continue
		}

		if potentialRival.UID == candidate.UID || !potentialRival.Conflict(candidate) {
			// if candidate and potentialRival are the same subnet or if they do
			// not conflict skip potentialRival.
			continue
		}

		// If we're here the two subnets represented by potentialRival and
		// candidate are in conflict, that is, they are rivals.
		if potentialRival.CIDRConflict(candidate) {
			klog.V(2).Infof("CIDR conflict found between %s (%d, %d) and %s (%d, %d).", candidate.NamespacedName, candidate.BaseU, candidate.LastU, potentialRival.NamespacedName, potentialRival.BaseU, potentialRival.LastU)
			conflictsMsgs = append(conflictsMsgs, fmt.Sprintf("CIDR overlaps with %s's (%s)", potentialRival.NamespacedName, pr.Spec.IPv4))
		}
		if potentialRival.NSConflict(candidate) {
			klog.V(2).Infof("Namespace conflict found between %s and %s.", candidate.NamespacedName, potentialRival.NamespacedName)
			conflictsMsgs = append(conflictsMsgs, fmt.Sprintf("same VNI but different namespace wrt %s", potentialRival.NamespacedName))
		}

		// Record the conflict in the conflicts cache.
		if err = v.recordConflict(potentialRival.NamespacedName, candidate.NamespacedName, potentialRival.UID); err != nil {
			return
		}
	}

	return
}

func (v *Validator) updateSubnetValidity(s1 *netv1a1.Subnet, validationErrors []string) error {
	// Check if s's status needs an update.
	sort.Strings(validationErrors)
	validated := len(validationErrors) == 0
	if s1.Status.Validated == validated && equal(s1.Status.Errors, validationErrors) {
		klog.V(4).Infof("%s/%s's status was not updated because it is already up to date.", s1.Namespace, s1.Name)
		v.duplicateWorkCount.Inc()
		return nil
	}

	s2 := s1.DeepCopy()
	s2.Status.Validated = validated
	s2.Status.Errors = validationErrors

	tBefore := time.Now()
	s3, err := v.netIfc.Subnets(s2.Namespace).Update(s2)
	tAfter := time.Now()
	v.subnetUpdateHistograms.With(prometheus.Labels{"err": FormatErrVal(err != nil), "statusErr": FormatErrVal(len(validationErrors) > 0)}).Observe(tAfter.Sub(tBefore).Seconds())
	switch {
	case err == nil:
		nsn := k8stypes.NamespacedName{
			Namespace: s1.Namespace,
			Name:      s1.Name,
		}
		klog.V(4).Infof("Recorded errors=%s and validated=%t into %s's status.", validationErrors, s2.Status.Validated, nsn)
		v.updateStaleRV(nsn, s1.ResourceVersion)
		if !s1.Status.Validated && len(s1.Status.Errors) == 0 {
			v.subnetCreateToValidatedHistograms.With(prometheus.Labels{"statusErr": FormatErrVal(len(validationErrors) > 0)}).Observe(tAfter.Sub(s1.CreationTimestamp.Time).Seconds())
		}
		if validated {
			v.eventRecorder.Event(s3, k8scorev1api.EventTypeNormal, "SubnetValidated", "")
		} else {
			v.eventRecorder.Eventf(s3, k8scorev1api.EventTypeWarning, "SubnetFailedValidation", "Found rival subnets: %s", strings.Join(validationErrors, ", "))
		}
	case malformedRequest(err):
		klog.Errorf("Failed update from %#+v to %#+v: %s; there will be no retry because of the nature of the error.", s1, s2, err.Error())
	default:
		return fmt.Errorf("failed update from %#+v to %#+v: %s", s1, s2, err.Error())
	}

	return nil
}

func (v *Validator) recordConflict(enrollerNSN, enrolleeNSN k8stypes.NamespacedName, enrollerUID k8stypes.UID) error {
	mismatchType := ""

	v.conflictsMutex.Lock()
	defer func() {
		v.conflictsMutex.Unlock()
		if mismatchType != "" {
			v.cacheVsLiveSubnetMismatches.With(prometheus.Labels{"mismatch_type": mismatchType}).Inc()
		}
	}()

	c := v.conflicts[enrollerNSN]
	if c == nil {
		// Either enroller has been deleted and its deletion has been processed
		// between the live list and now, or its creation has not been processed
		// yet. Retry after some time so that the ambiguity is resolved.
		mismatchType = missingCacheMismatch
		return fmt.Errorf("registration as %s's rival failed: conflicts cache not found", enrollerNSN)
	}
	if c.ownerUID != enrollerUID {
		// The conflicts cache does not belong to the enroller, but to another
		// subnet with the same namespaced name. One possible reason is that
		// such subnet has been deleted and the creation of the enroller
		// followed but the validator has not processed these events yet (we got
		// the enroller with a live list from the API server). Another
		// possibility is the opposite: after the enroller was received from the
		// API server it's been deleted and another subnet with the same
		// namespaced name has been created and processed by this controller.
		// Since it is not known what happened, return an error that will
		// trigger a delayed retry. Hopefully time will resolve the ambiguity.
		mismatchType = cacheOwnerMismatch
		return fmt.Errorf("registration as %s's rival failed: mismatch between live data (enroller's UID: %s) and conflicts cache data (owner's UID: %s)", enrollerNSN, enrollerUID, c.ownerUID)
	}

	c.rivals = append(c.rivals, enrolleeNSN)
	return nil
}

func (v *Validator) updateStaleRV(s k8stypes.NamespacedName, rv string) {
	v.staleRVsMutex.Lock()
	defer v.staleRVsMutex.Unlock()

	v.staleRVs[s] = rv
}

// TODO move to shared util pkg
func equal(s1, s2 []string) bool {
	if len(s1) != len(s2) {
		return false
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			return false
		}
	}
	return true
}

// TODO move to shared util pkg
func FormatErrVal(err bool) string {
	if err {
		return "err"
	}
	return "ok"
}
