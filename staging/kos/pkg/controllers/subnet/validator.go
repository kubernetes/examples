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
	"net"
	"sync"
	"time"

	"github.com/golang/glog"

	k8scorev1api "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	k8sutilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8swait "k8s.io/apimachinery/pkg/util/wait"
	k8scache "k8s.io/client-go/tools/cache"
	k8sworkqueue "k8s.io/client-go/util/workqueue"

	netv1a1 "k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
	kosclientv1a1 "k8s.io/examples/staging/kos/pkg/client/clientset/versioned/typed/network/v1alpha1"
	netlistv1a1 "k8s.io/examples/staging/kos/pkg/client/listers/network/v1alpha1"
	kosctlrutils "k8s.io/examples/staging/kos/pkg/controllers/utils"
)

// TODO: Add Prometheus metrics.

const (
	// These fields are the reasons written to subnet's conflict conditions.
	cidrAndNSReason = "cidrAndNamespaceConflict"
	cidrReason      = "cidrConflict"
	nsReason        = "namespaceConflict"
)

// conflictsCache holds information for one subnet regarding conflicts with
// other subnets. There's no guarantee that the cache is up-to-date: a subnet Y
// stored in it might no longer be in conflict with the owning subnet, for
// instance because of an update to Y's CIDR following its addition to the
// cache.
type conflictsCache struct {
	// ownerData stores the data relevant to validation for the subnet owning
	// the conflicts cache.
	ownerData *subnetData

	// rivals is the list of namespaced names of the subnets that were observed
	// to conflict with the subnet owning the conflicts cache. If subnet X is
	// the owner of the conflicts cache and a subnet Y is in rivals this means
	// that while a queue worker was working on Y it found a conflict with X and
	// at that time X had the VNI and CIDR values in ownerData.
	rivals []k8stypes.NamespacedName
}

// Validator performs validation for newly-created or updated subnets, and
// writes in their status the outcome of the validation. Validation consists of
// two checks:
//
// 		(1) CIDRs for subnets with the same VNI are disjoint.
// 		(2) all subnets with the same VNI are within the same K8s namespace.
//
// If a subnet S1 does not pass validation because of a conflict with another
// subnet S2, upon deletion of S2 S1 is validated again.
// Validator uses an informer on Subnets to be notified of creation or updates,
// but does a live list against the API server to retrieve the conflicting
// subnets when validating a subnet, to avoid race conditions caused by multiple
// validators running at the same time.
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
	// hence processing is immediately stopped.
	// Only access while holding staleRVsMutex.
	staleRVs      map[k8stypes.NamespacedName]string
	staleRVsMutex sync.Mutex
}

func NewValidator(netIfc kosclientv1a1.NetworkV1alpha1Interface,
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

	v.subnetInformer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
		AddFunc:    v.OnSubnetCreate,
		UpdateFunc: v.OnSubnetUpdate,
		DeleteFunc: v.OnSubnetDelete},
	)

	go v.subnetInformer.Run(stop)
	glog.V(2).Infof("Informer run forked.")

	if !k8scache.WaitForCacheSync(stop, v.subnetInformer.HasSynced) {
		return errors.New("cache failed to sync")
	}
	glog.V(2).Infof("Cache synced.")

	// Start workers.
	for i := 0; i < v.workers; i++ {
		go k8swait.Until(v.processQueue, time.Second, stop)
	}
	glog.V(2).Infof("Launched %d workers.", v.workers)

	<-stop
	return nil
}

func (v *Validator) OnSubnetCreate(obj interface{}) {
	s := obj.(*netv1a1.Subnet)
	glog.V(5).Infof("Notified of creation of %#+v.", s)
	subnetRef := k8stypes.NamespacedName{
		Namespace: s.Namespace,
		Name:      s.Name,
	}
	v.queue.Add(subnetRef)
}

func (v *Validator) OnSubnetUpdate(oldObj, newObj interface{}) {
	oldS, newS := oldObj.(*netv1a1.Subnet), newObj.(*netv1a1.Subnet)
	glog.V(5).Infof("Notified of update from %#+v to %#+v.", oldS, newS)
	subnetRef := k8stypes.NamespacedName{
		Namespace: newS.Namespace,
		Name:      newS.Name,
	}
	// Process a subnet only if the fields that affect validation have changed.
	if oldS.Spec.IPv4 != newS.Spec.IPv4 || oldS.Spec.VNI != newS.Spec.VNI {
		v.queue.Add(subnetRef)
	}
}

func (v *Validator) OnSubnetDelete(obj interface{}) {
	s := kosctlrutils.Peel(obj).(*netv1a1.Subnet)
	glog.V(5).Infof("Notified of deletion of %#+v.", s)
	subnetRef := k8stypes.NamespacedName{
		Namespace: s.Namespace,
		Name:      s.Name,
	}
	v.queue.Add(subnetRef)
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
		glog.Errorf("subnet lister failed to lookup %s: %s", subnetNSN, err.Error())
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

	rivals := v.clearConflictsCache(s)

	// Enqueue old rivals so that they can be re-validated: they might no longer
	// have conflicts as this subnet has been deleted.
	for _, r := range rivals {
		v.queue.Add(r)
	}
}

func (v *Validator) processExistingSubnet(s *netv1a1.Subnet) error {
	sd, err := parseSubnet(s)
	if err != nil {
		return err
	}

	if v.subnetIsStale(sd.namespacedName, s.ResourceVersion) {
		return nil
	}

	// If we're here s might have been created or updated in a way that affects
	// validation. We need to update its conflicts cache accordingly and
	// reconsider old rivals because they might no longer be in conflict with
	// s.
	oldRivals := v.updateConflictsCache(sd)
	for _, r := range oldRivals {
		v.queue.Add(r)
	}

	// When a subnet is validated, in case of conflicts it is added to the
	// conflicts caches of its rival subnets, so that when such rivals are
	// deleted it can be revalidated. This is useless for a subnet that has
	// already passed validation. Also, if this controllers sees
	// s.Status.Validated to be true, there's no way that such value can refer
	// to a previous, stale version of the subnet, because the API server clears
	// s.Status.Validated when a subnet spec is updated in a way that requires
	// revalidation. Hence, we can simply stop processing here if
	// s.Status.Validated is true.
	// ? Maybe we should make it clearer somewhere (e.g. API types comments and
	// doc) that the API server clears s.Status.Validated when a subnet spec is
	// updated in a way that requires revalidation.
	if s.Status.Validated {
		return nil
	}

	// Retrieve all the other subnets. Doing a live list as opposed to a
	// cache-based one (through the informer) prevents race conditions that can
	// arise in case of multiple validators running.
	allSubnets, err := v.netIfc.Subnets(k8scorev1api.NamespaceAll).List(k8smetav1.ListOptions{})
	if err != nil && doNotRetryList(err) {
		glog.Errorf("live list of all subnets against API server failed while validating %s: %s. There will be no retry because of the nature of the error", sd.namespacedName, err.Error())
		// This should never happen, no point in retrying.
		return nil
	}
	if err != nil {
		return fmt.Errorf("live list of all subnets against API server failed: %s", err.Error())
	}

	// Look for conflicts with all the other subnets and record them in the
	// rivals conflicts caches.
	conflictsMsgs, conflictFound, err := v.recordConflicts(sd, allSubnets.Items)
	if err != nil {
		return err
	}

	if err := v.updateSubnetValidity(s, !conflictFound, conflictsMsgs); err != nil {
		return fmt.Errorf("failed to write validation outcome into %s's status: %s", sd.namespacedName, err.Error())
	}

	return nil
}

func (v *Validator) clearStaleRV(s k8stypes.NamespacedName) {
	v.staleRVsMutex.Lock()
	defer v.staleRVsMutex.Unlock()

	delete(v.staleRVs, s)
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

func parseSubnet(s *netv1a1.Subnet) (*subnetData, error) {
	_, ipNet, err := net.ParseCIDR(s.Spec.IPv4)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %#+v: %s", s, err.Error())
	}
	sd := &subnetData{
		namespacedName: k8stypes.NamespacedName{
			Namespace: s.Namespace,
			Name:      s.Name,
		},
		vni:   s.Spec.VNI,
		baseU: ipv4ToUint32(ipNet.IP),
	}
	ones, bits := ipNet.Mask.Size()
	delta := uint32(uint64(1)<<uint(bits-ones) - 1)
	sd.lastU = sd.baseU + delta
	return sd, nil
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

func (v *Validator) updateConflictsCache(s *subnetData) []k8stypes.NamespacedName {
	v.conflictsMutex.Lock()
	defer v.conflictsMutex.Unlock()

	c := v.conflicts[s.namespacedName]
	if c == nil {
		c = &conflictsCache{
			ownerData: s,
			rivals:    make([]k8stypes.NamespacedName, 0),
		}
		v.conflicts[s.namespacedName] = c
		return nil
	}

	var oldRivals []k8stypes.NamespacedName
	if s.vni != c.ownerData.vni || !s.contains(c.ownerData) {
		// The data that affects validation changed, return all rivals so that
		// they can be re-validated again as the conflict might have disappeared.
		oldRivals = c.rivals
		c.rivals = make([]k8stypes.NamespacedName, 0)
	}
	c.ownerData = s

	return oldRivals
}

func doNotRetryList(e error) bool {
	return k8serrors.IsUnauthorized(e) ||
		k8serrors.IsBadRequest(e) ||
		k8serrors.IsForbidden(e) ||
		k8serrors.IsNotAcceptable(e) ||
		k8serrors.IsUnsupportedMediaType(e) ||
		k8serrors.IsMethodNotSupported(e)
}

func (v *Validator) recordConflicts(candidate *subnetData, potentialRivals []netv1a1.Subnet) ([]string, bool, error) {
	var conflictsMsgs []string
	var conflictFound bool

	for _, pr := range potentialRivals {
		potentialRival, err := parseSubnet(&pr)
		if err != nil {
			glog.Errorf("parsing %s failed while validating %s: %s", potentialRival.namespacedName, candidate.namespacedName, err.Error())
		}

		if !potentialRival.conflict(candidate) || potentialRival.sameSubnetAs(candidate) {
			// potentialRival is not a rival to candidate or it is the same
			// subnet as candidate, hence we skip it.
			continue
		}

		// If we're here the two subnets represented by potentialRival and
		// candidate are in conflict, that is, they are rivals.
		conflictFound = true
		if potentialRival.cidrConflict(candidate) {
			glog.V(2).Infof("CIDR conflict found between %s (%d, %d) and %s (%d, %d).", candidate.namespacedName, candidate.baseU, candidate.lastU, potentialRival.namespacedName, potentialRival.baseU, potentialRival.lastU)
			conflictsMsgs = append(conflictsMsgs, fmt.Sprintf("CIDR overlaps with %s's (%s)", potentialRival.namespacedName, pr.Spec.IPv4))
		}
		if potentialRival.nsConflict(candidate) {
			glog.V(2).Infof("Namespace conflict found between %s and %s.", candidate.namespacedName, potentialRival.namespacedName)
			conflictsMsgs = append(conflictsMsgs, fmt.Sprintf("same VNI but different namespace wrt %s", potentialRival.namespacedName))
		}

		// Record the conflict in the conflicts cache.
		if err = v.recordConflict(potentialRival, candidate); err != nil {
			return conflictsMsgs, conflictFound, err
		}
	}

	return conflictsMsgs, conflictFound, nil
}

func (v *Validator) updateSubnetValidity(s *netv1a1.Subnet, validated bool, validationErrors []string) error {
	sCopy := s.DeepCopy()

	sCopy.Status.Validated = validated
	sCopy.Status.Errors.Validation = validationErrors

	_, err := v.netIfc.Subnets(sCopy.Namespace).Update(sCopy)
	switch {
	case err == nil:
		nsn := k8stypes.NamespacedName{
			Namespace: s.Namespace,
			Name:      s.Name,
		}
		v.updateStaleRV(nsn, s.ResourceVersion)
	case doNotRetryUpdate(err):
		glog.Errorf("failed to update subnet from %#+v to %#+v: %s. There will be no retry because of the nature of the error", s, sCopy, err.Error())
	case !doNotRetryUpdate(err):
		return fmt.Errorf("failed to update subnet from %#+v to %#+v: %s", s, sCopy, err.Error())
	}

	return nil
}

func (v *Validator) recordConflict(enroller, enrollee *subnetData) error {
	v.conflictsMutex.Lock()
	defer v.conflictsMutex.Unlock()

	c := v.conflicts[enroller.namespacedName]

	if c == nil {
		return fmt.Errorf("registration of %s as a rival of %s failed: %s's conflicts cache not found", enrollee.namespacedName, enroller.namespacedName, enroller.namespacedName)
	}

	if enroller.differs(c.ownerData) {
		// If we're here the version of the enroller recorded in its conflicts
		// cache does not match the version of the enroller we got with the live
		// list to the API server: one of the two is stale. Return an error so
		// that the caller can wait a little bit and retry (hopefully the
		// version skew has resolved by then).
		return fmt.Errorf("registration of %s as a rival of %s failed: mismatch between %s's live data (%#+v) and conflicts cache data (%#+v)", enrollee.namespacedName, enroller.namespacedName, enroller.namespacedName, enroller, c.ownerData)
	}

	c.rivals = append(c.rivals, enrollee.namespacedName)
	return nil
}

func (v *Validator) updateStaleRV(s k8stypes.NamespacedName, rv string) {
	v.staleRVsMutex.Lock()
	defer v.staleRVsMutex.Unlock()

	v.staleRVs[s] = rv
}

func doNotRetryUpdate(e error) bool {
	return k8serrors.IsUnauthorized(e) ||
		k8serrors.IsBadRequest(e) ||
		k8serrors.IsForbidden(e) ||
		k8serrors.IsNotAcceptable(e) ||
		k8serrors.IsUnsupportedMediaType(e) ||
		k8serrors.IsMethodNotSupported(e) ||
		k8serrors.IsInvalid(e) ||
		k8serrors.IsGone(e)
}

// subnetData is the subset of the fields of a subnet relevant to determine
// whether there are conflicts with other subnets.
type subnetData struct {
	namespacedName    k8stypes.NamespacedName
	vni, baseU, lastU uint32
}

func ipv4ToUint32(ip net.IP) uint32 {
	v4 := ip.To4()
	return uint32(v4[0])<<24 + uint32(v4[1])<<16 + uint32(v4[2])<<8 + uint32(v4[3])
}

func (s1 *subnetData) contains(s2 *subnetData) bool {
	return s1.baseU <= s2.baseU && s1.lastU >= s2.lastU
}

func (s1 *subnetData) differs(s2 *subnetData) bool {
	return s1.vni != s2.vni || s1.baseU != s2.baseU || s1.lastU != s2.lastU
}

func (s1 *subnetData) sameSubnetAs(s2 *subnetData) bool {
	return s1.namespacedName == s2.namespacedName
}

func (s1 *subnetData) conflict(s2 *subnetData) bool {
	return s1.vni == s2.vni && (s1.namespacedName.Namespace != s2.namespacedName.Namespace || (s1.baseU <= s2.lastU && s1.lastU >= s2.baseU))
}

func (s1 *subnetData) cidrConflict(s2 *subnetData) bool {
	return s1.vni == s2.vni && s1.baseU <= s2.lastU && s1.lastU >= s2.baseU
}

func (s1 *subnetData) nsConflict(s2 *subnetData) bool {
	return s1.vni == s2.vni && s1.namespacedName.Namespace != s2.namespacedName.Namespace
}
