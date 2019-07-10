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
	"sync"
	"time"

	"github.com/golang/glog"

	k8scorev1api "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfields "k8s.io/apimachinery/pkg/fields"
	k8stypes "k8s.io/apimachinery/pkg/types"
	k8sutilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8swait "k8s.io/apimachinery/pkg/util/wait"
	k8scache "k8s.io/client-go/tools/cache"
	k8sworkqueue "k8s.io/client-go/util/workqueue"

	netv1a1 "k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
	kosclientv1a1 "k8s.io/examples/staging/kos/pkg/client/clientset/versioned/typed/network/v1alpha1"
	netlistv1a1 "k8s.io/examples/staging/kos/pkg/client/listers/network/v1alpha1"
	"k8s.io/examples/staging/kos/pkg/util/parse"
	"k8s.io/examples/staging/kos/pkg/util/parse/network/subnet"
)

// TODO: Add Prometheus metrics.

const subnetVNIField = "spec.vni"

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
}

func NewValidationController(netIfc kosclientv1a1.NetworkV1alpha1Interface,
	subnetInformer k8scache.SharedInformer,
	subnetLister netlistv1a1.SubnetLister,
	queue k8sworkqueue.RateLimitingInterface,
	workers int) *Validator {

	return &Validator{
		netIfc:         netIfc,
		subnetInformer: subnetInformer,
		subnetLister:   subnetLister,
		queue:          queue,
		workers:        workers,
		conflicts:      make(map[k8stypes.NamespacedName]*conflictsCache),
		staleRVs:       make(map[k8stypes.NamespacedName]string),
	}
}

// Run starts the validator and blocks until stop is closed. This entails
// starting its Informer and the worker goroutines.
func (v *Validator) Run(stop <-chan struct{}) error {
	defer k8sutilruntime.HandleCrash()
	defer v.queue.ShutDown()

	glog.Info("Starting subnet validation controller.")
	defer glog.Info("Shutting down subnet validation controller.")

	v.subnetInformer.AddEventHandler(v)

	if !k8scache.WaitForCacheSync(stop, v.subnetInformer.HasSynced) {
		return errors.New("informer cache failed to sync")
	}
	glog.V(2).Infof("Informer cache synced.")

	// Start workers.
	for i := 0; i < v.workers; i++ {
		go k8swait.Until(v.processQueue, time.Second, stop)
	}
	glog.V(2).Infof("Launched %d workers.", v.workers)

	<-stop

	return nil
}

func (v *Validator) OnAdd(obj interface{}) {
	s := obj.(*netv1a1.Subnet)
	glog.V(5).Infof("Notified of creation of %#+v.", s)
	v.queue.Add(k8stypes.NamespacedName{
		Namespace: s.Namespace,
		Name:      s.Name,
	})
}

func (v *Validator) OnUpdate(oldObj, newObj interface{}) {
	oldS, newS := oldObj.(*netv1a1.Subnet), newObj.(*netv1a1.Subnet)
	glog.V(5).Infof("Notified of update from %#+v to %#+v.", oldS, newS)

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
	glog.V(5).Infof("Notified of deletion of %#+v.", s)
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
		glog.Warningf("Failed processing %s, requeuing (%d earlier requeues): %s.", subnet, requeues, err.Error())
		v.queue.AddRateLimited(subnet)
		return
	}
	glog.V(4).Infof("Finished %s with %d requeues.", subnet, requeues)
	v.queue.Forget(subnet)
}

func (v *Validator) processSubnet(subnetNSN k8stypes.NamespacedName) error {
	subnet, err := v.subnetLister.Subnets(subnetNSN.Namespace).Get(subnetNSN.Name)

	if err != nil && !k8serrors.IsNotFound(err) {
		glog.Errorf("Subnet lister failed to lookup %s: %s.", subnetNSN, err.Error())
		// This should never happen. No point in retrying.
		return nil
	}

	if k8serrors.IsNotFound(err) {
		v.processDeletedSubnet(subnetNSN)
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
		glog.Errorf("Subnet %s/%s is malformed: %s. This should never happen.", s.Namespace, s.Name, parsingErrs.Error())
		return nil
	}

	if v.subnetIsStale(ss.NamespacedName, s.ResourceVersion) {
		glog.V(5).Infof("Stopping processing of %s because it's stale. Processing will be restarted upon receiving the fresh version.", ss.NamespacedName)
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
	potentialRivals, err := v.netIfc.Subnets(k8scorev1api.NamespaceAll).List(k8smetav1.ListOptions{
		FieldSelector: k8sfields.OneTermEqualSelector(subnetVNIField, strconv.FormatUint(uint64(ss.VNI), 10)).String(),
	})
	if err != nil {
		if malformedRequest(err) {
			glog.Errorf("live list of all subnets against API server failed while validating %s: %s. There will be no retry because of the nature of the error", ss.NamespacedName, err.Error())
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
			glog.Errorf("Found malformed subnet %s/%s while validating %s: %s. This should never happen.", pr.Namespace, pr.Name, candidate.NamespacedName, parsingErrs.Error())
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
			glog.V(2).Infof("CIDR conflict found between %s (%d, %d) and %s (%d, %d).", candidate.NamespacedName, candidate.BaseU, candidate.LastU, potentialRival.NamespacedName, potentialRival.BaseU, potentialRival.LastU)
			conflictsMsgs = append(conflictsMsgs, fmt.Sprintf("CIDR overlaps with %s's (%s)", potentialRival.NamespacedName, pr.Spec.IPv4))
		}
		if potentialRival.NSConflict(candidate) {
			glog.V(2).Infof("Namespace conflict found between %s and %s.", candidate.NamespacedName, potentialRival.NamespacedName)
			conflictsMsgs = append(conflictsMsgs, fmt.Sprintf("same VNI but different namespace wrt %s", potentialRival.NamespacedName))
		}

		// Record the conflict in the conflicts cache.
		if err = v.recordConflict(potentialRival.NamespacedName, candidate.NamespacedName, potentialRival.UID); err != nil {
			return
		}
	}

	return
}

func (v *Validator) updateSubnetValidity(s *netv1a1.Subnet, validationErrors []string) error {
	// Check if s's status needs an update.
	sort.Strings(validationErrors)
	validated := len(validationErrors) == 0
	if s.Status.Validated == validated && equal(s.Status.Errors, validationErrors) {
		glog.V(4).Infof("%s/%s's status was not updated because it is already up to date.", s.Namespace, s.Name)
		return nil
	}

	sCopy := s.DeepCopy()
	sCopy.Status.Validated = validated
	sCopy.Status.Errors = validationErrors

	_, err := v.netIfc.Subnets(sCopy.Namespace).Update(sCopy)
	switch {
	case err == nil:
		nsn := k8stypes.NamespacedName{
			Namespace: s.Namespace,
			Name:      s.Name,
		}
		glog.V(4).Infof("Recorded errors=%s and validated=%t into %s's status.", validationErrors, sCopy.Status.Validated, nsn)
		v.updateStaleRV(nsn, s.ResourceVersion)
	case malformedRequest(err):
		glog.Errorf("Failed update from %#+v to %#+v: %s; there will be no retry because of the nature of the error.", s, sCopy, err.Error())
	default:
		return fmt.Errorf("failed update from %#+v to %#+v: %s", s, sCopy, err.Error())
	}

	return nil
}

func (v *Validator) recordConflict(enrollerNSN, enrolleeNSN k8stypes.NamespacedName, enrollerUID k8stypes.UID) error {
	v.conflictsMutex.Lock()
	defer v.conflictsMutex.Unlock()

	c := v.conflicts[enrollerNSN]
	if c == nil {
		// Either enroller has been deleted and its deletion has been processed
		// between the live list and now, or its creation has not been processed
		// yet. Retry after some time so that the ambiguity is resolved.
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
