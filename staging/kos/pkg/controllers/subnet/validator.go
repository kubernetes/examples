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

// TODO: something which makes the validation failure visible to the client
// (event, Prometheus, status field, all of them, etc...).
// TODO: some log messages don't print the specific values that yield problems,
// only what the problem is (e.g. "CIDRs overlap", but no CIDR values). Add such
// values (this might require changing some functions signatures).

// usable is the value the validator sets a subnet's status.ValidationOutcome to
// if there are no conflicts with other subnets.
const usable = "usable"

// conflictsCache holds information for one subnet regarding conflicts with
// other subnets. It stores three mutually exclusive caches for CIDR-only
// conflicts, namespace-only conflicts and CIDR and namespace conflicts.
// There's no guarantee that the cache is up-to-date: a rival subnet stored in
// it might no longer be a rival because of an update.
type conflictsCache struct {
	// ownerData stores the VNI, lowest and highest addresses of the subnet
	// owning the conflictsCache.
	ownerData *subnetData

	// cidrRivals is the list of subnets whose CIDRs overlap with the subnet
	// owning the conflictsCache when its VNI, lowest and highest addresses have
	// the values stored in ownerData.
	cidrRivals []k8stypes.NamespacedName

	// namespaceRivals is the list of subnets with same VNI but different K8s
	// API namespaces wrt the subnet owning the conflictsCache when its VNI,
	// lowest and highest addresses have the values stored in ownerData.
	namespaceRivals []k8stypes.NamespacedName

	// cidrAndNamespaceRivals is the list of subnets with same VNI, different
	// K8s API namespace and overlapping CIDRs wrt the subnet owning the
	// conflictsCache when its VNI, lowest and highest addresses have the values
	// stored in ownerData.
	cidrAndNamespaceRivals []k8stypes.NamespacedName
}

// Validator performs validation for newly-created or updated subnets, and sets
// their status.ValidationOutcome to the outcome of the validation. Validation
// consists of two checks:
//
// 		(1) CIDRs for subnets with the same VNI are disjoint.
// 		(2) all subnets with the same VNI are within the same K8s namespace.
//
// If a subnet does not pass validation, no status field is set and no action is
// taken.
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
}

// NewValidator returns a new Validator.
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

// OnSubnetCreate is invoked when a subnet is added to the Validator's
// informer's cache. It enqueues a reference to the subnet.
func (v *Validator) OnSubnetCreate(obj interface{}) {
	s := obj.(*netv1a1.Subnet)
	glog.V(5).Infof("Notified of creation of %#+v.", s)
	sRef := k8stypes.NamespacedName{
		Namespace: s.Namespace,
		Name:      s.Name,
	}
	v.queue.Add(sRef)
}

// OnSubnetUpdate is invoked when a subnet in the Validator's informer's cache
// is updated. It enqueues a reference to the subnet if the update is relevant
// to the Validator.
func (v *Validator) OnSubnetUpdate(oldObj, newObj interface{}) {
	oldS, newS := oldObj.(*netv1a1.Subnet), newObj.(*netv1a1.Subnet)
	glog.V(5).Infof("Notified of update from %#+v to %#+v.", oldS, newS)
	sRef := k8stypes.NamespacedName{
		Namespace: newS.Namespace,
		Name:      newS.Name,
	}
	// Process a subnet only if the fields that affect validation have changed.
	if oldS.Spec.IPv4 != newS.Spec.IPv4 || oldS.Spec.VNI != newS.Spec.VNI {
		v.queue.Add(sRef)
	}
}

// OnSubnetDelete is invoked when a subnet is removed from the Validator's
// informer's cache. It enqueues a reference to the subnet.
func (v *Validator) OnSubnetDelete(obj interface{}) {
	s := kosctlrutils.Peel(obj).(*netv1a1.Subnet)
	glog.V(5).Infof("Notified of deletion of %#+v.", s)
	sRef := k8stypes.NamespacedName{
		Namespace: s.Namespace,
		Name:      s.Name,
	}
	v.queue.Add(sRef)
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
	err := v.processSubnet(subnet.Namespace, subnet.Name)
	requeues := v.queue.NumRequeues(subnet)
	if err == nil {
		glog.V(4).Infof("Finished %s with %d requeues.", subnet, requeues)
		v.queue.Forget(subnet)
		return
	}
	glog.Warningf("Failed processing %s, requeuing (%d earlier requeues): %s.", subnet, requeues, err.Error())
	v.queue.AddRateLimited(subnet)
}

func (v *Validator) processSubnet(namespace, name string) error {
	s, err := v.subnetLister.Subnets(namespace).Get(name)
	if err != nil && !k8serrors.IsNotFound(err) {
		glog.Errorf("subnet lister failed to lookup %s/%s: %s", namespace, name, err.Error())
		// This should never happen. No point in retrying.
		return nil
	}
	if k8serrors.IsNotFound(err) {
		v.processDeletedSubnet(namespace, name)
		return nil
	}
	return v.processExistingSubnet(s)
}

func (v *Validator) processDeletedSubnet(namespace, name string) {
	rivals := v.clearConflictsCache(k8stypes.NamespacedName{
		Namespace: namespace,
		Name:      name,
	})
	// re-validate old rivals: they might no longer have conflicts as this
	// subnet has been deleted.
	for _, r := range rivals {
		v.queue.Add(r)
	}
}

func (v *Validator) processExistingSubnet(s *netv1a1.Subnet) error {
	sd, err := parseSubnet(s)
	if err != nil {
		return err
	}
	nsn := k8stypes.NamespacedName{
		Namespace: s.Namespace,
		Name:      s.Name,
	}
	// If we're here s has either been created or updated in a way that affects
	// validation. We need to update its conflicts cache accordingly.
	oldRivals := v.updateConflictsCache(nsn, sd)
	// Reconsider old rivals because s has changed and they might no longer be
	// in conflict.
	for _, r := range oldRivals {
		v.queue.Add(r)
	}

	if s.Status.ValidationOutcome == usable {
		// When a subnet is validated, in case of conflicts it is added to the
		// conflicts caches of its rival subnets, so that when such rivals are
		// deleted it can be revalidated. If a subnet is usable its revalidation
		// is useless. Even if the subnet is updated and the update clears its
		// usability, the subnet will be reprocessed and registered in the
		// rivals cache when that happens. Hence, if the subnet is usable
		// there's nothing else to do.
		return nil
	}

	subnets, err := v.netIfc.Subnets(k8scorev1api.NamespaceAll).List(k8smetav1.ListOptions{})
	if err != nil && doNotRetryList(err) {
		glog.Errorf("live list of all subnets against API server failed while validating %s: %s. There will be no retry because of the nature of the error", nsn, err.Error())
		// This should never happen, no point in retrying.
		return nil
	}
	if err != nil {
		return fmt.Errorf("live list of all subnets against API server failed: %s", err.Error())
	}

	// Look for conflicts with all the other subnets and record them in the
	// rivals conflicts caches.
	conflictsFound, err := v.recordConflicts(nsn, sd, subnets.Items)
	if err != nil {
		return err
	}
	if conflictsFound {
		return nil
	}
	// If we're here no conflict was found and the subnet status can be updated
	// to mark it as usable by other components.
	if err = v.approveSubnet(s); err != nil {
		return err
	}
	glog.V(2).Infof("Subnet %#+v has no conflicts and was marked as usable.", s)
	return nil
}

func (v *Validator) clearConflictsCache(subnet k8stypes.NamespacedName) []k8stypes.NamespacedName {
	v.conflictsMutex.Lock()
	defer v.conflictsMutex.Unlock()
	c := v.conflicts[subnet]
	delete(v.conflicts, subnet)
	if c == nil {
		return nil
	}
	return append(c.cidrAndNamespaceRivals, append(c.namespaceRivals, c.cidrRivals...)...)
}

func (v *Validator) updateConflictsCache(subnetNSN k8stypes.NamespacedName, sd *subnetData) []k8stypes.NamespacedName {
	v.conflictsMutex.Lock()
	defer v.conflictsMutex.Unlock()
	c := v.conflicts[subnetNSN]
	if c == nil {
		c = &conflictsCache{
			ownerData:              sd,
			cidrRivals:             make([]k8stypes.NamespacedName, 0),
			namespaceRivals:        make([]k8stypes.NamespacedName, 0),
			cidrAndNamespaceRivals: make([]k8stypes.NamespacedName, 0),
		}
		v.conflicts[subnetNSN] = c
		return nil
	}
	var oldRivals []k8stypes.NamespacedName
	if !sd.contains(c.ownerData) {
		// The owner CIDR changed, reconsider all CIDR rivals as the conflict
		// might have disappeared.
		oldRivals = append(oldRivals, c.cidrRivals...)
		c.cidrRivals = make([]k8stypes.NamespacedName, 0)
	}
	if sd.vni != c.ownerData.vni {
		// The owner VNI changed, reconsider all Namespace rivals as the
		// conflict might have disappeared.
		oldRivals = append(oldRivals, c.namespaceRivals...)
		c.namespaceRivals = make([]k8stypes.NamespacedName, 0)
	}
	if !sd.contains(c.ownerData) && sd.vni != c.ownerData.vni {
		// Both the owner VNI and CIDR changed, reconsider all Namespace and VNI
		// rivals as the conflict might have disappeared.
		oldRivals = append(oldRivals, c.cidrAndNamespaceRivals...)
		c.cidrAndNamespaceRivals = make([]k8stypes.NamespacedName, 0)
	}
	c.ownerData = sd
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

func (v *Validator) recordConflicts(nsn k8stypes.NamespacedName, sd *subnetData, subnets []netv1a1.Subnet) (bool, error) {
	var conflictsFound bool
	for _, subnet := range subnets {
		if (nsn.Namespace == subnet.Namespace && nsn.Name == subnet.Name) || sd.vni != subnet.Spec.VNI {
			// Do not compare a subnet against itself. Also, conflicts only
			// exist between subnets with the same VNI. If that's not the case
			// we skip this subnet.
			continue
		}

		// Check whether there's a namespace conflict.
		subnetNSN := k8stypes.NamespacedName{
			Namespace: subnet.Namespace,
			Name:      subnet.Name,
		}
		namespacesDiffer := nsn.Namespace != subnet.Namespace
		if namespacesDiffer {
			// Subnets with the same VNI must be within the same namespace.
			glog.V(2).Infof("Conflict: %s and %s both have vni %d. Subnets with the same VNI must be within the same K8s namespace.", nsn, subnetNSN, sd.vni)
		}

		// Check whether there's a CIDR conflict.
		subnetData, err := parseSubnet(&subnet)
		if err != nil {
			glog.Errorf("parsing %s failed while validating %s: %s", subnetNSN, nsn, err.Error())
		}
		cidrsOverlap := err == nil && sd.overlaps(subnetData)
		if cidrsOverlap {
			// Subnets with the same VNI must have disjoint CIDRs.
			glog.V(2).Infof("Conflict: %s and %s in vni %d have overlapping CIDRs. Subnets with the same VNI must have disjoint CIDRs.", nsn, subnetNSN, sd.vni)
		}

		if !(namespacesDiffer || cidrsOverlap) {
			continue
		}
		conflictsFound = true

		// Record the conflict in the conflicts cache.
		if err = v.recordConflict(namespacesDiffer, cidrsOverlap, subnetNSN, nsn, sd); err != nil {
			return conflictsFound, err
		}
	}

	return conflictsFound, nil
}

func (v *Validator) approveSubnet(s *netv1a1.Subnet) error {
	sCopy := s.DeepCopy()
	sCopy.Status.ValidationOutcome = usable
	_, err := v.netIfc.Subnets(sCopy.Namespace).Update(sCopy)
	if err != nil && !doNotRetryUpdate(err) {
		return fmt.Errorf("failed to update subnet from %#+v to %#+v: %s", s, sCopy, err.Error())
	}
	if doNotRetryUpdate(err) {
		glog.Errorf("failed to update subnet from %#+v to %#+v: %s. There will be no retry because of the nature of the error", s, sCopy, err.Error())
	}
	return nil
}

func (v *Validator) recordConflict(namespacesDiffer, cidrsOverlap bool,
	enroller, enrollee k8stypes.NamespacedName,
	enrolleeData *subnetData) (e error) {

	switch {
	case namespacesDiffer && cidrsOverlap:
		// Record the conflict in the Namespace and CIDR conflicts cache.
		e = v.recordCIDRAndNamespaceConflict(enroller, enrollee, enrolleeData)
	case namespacesDiffer:
		// Record the conflict in the Namespace conflicts cache.
		e = v.recordNamespaceConflict(enroller, enrollee, enrolleeData)
	case cidrsOverlap:
		// Record the conflict in the CIDR conflicts cache.
		e = v.recordCIDRConflict(enroller, enrollee, enrolleeData)
	}
	return
}

func (v *Validator) recordCIDRAndNamespaceConflict(enroller, enrollee k8stypes.NamespacedName, enrolleeData *subnetData) error {
	v.conflictsMutex.Lock()
	defer v.conflictsMutex.Unlock()
	c := v.conflicts[enroller]
	if c == nil {
		return fmt.Errorf("registration of %s as a rival of %s failed: %s's conflicts cache not found", enrollee, enroller, enroller)
	}
	if !(enrolleeData.overlaps(c.ownerData) && enrolleeData.vni == c.ownerData.vni) {
		errMsg := fmt.Sprintf("registration of %s as a CIDR and namespace rival of %s failed", enrollee, enroller)
		if !enrolleeData.overlaps(c.ownerData) {
			errMsg = fmt.Sprintf("%s, %s's CIDR in conflicts cache does not overlap with %s's CIDR", errMsg, enroller, enrollee)
		}
		if enrolleeData.vni != c.ownerData.vni {
			errMsg = fmt.Sprintf("%s, VNI in %s's conflicts cache is the same as %s's VNI (%d)", errMsg, enroller, enrollee, enrolleeData.vni)
		}
		return errors.New(errMsg)
	}
	c.cidrAndNamespaceRivals = append(c.cidrAndNamespaceRivals, enrollee)
	return nil
}

func (v *Validator) recordNamespaceConflict(enroller, enrollee k8stypes.NamespacedName, enrolleeData *subnetData) error {
	v.conflictsMutex.Lock()
	defer v.conflictsMutex.Unlock()
	c := v.conflicts[enroller]
	if c == nil {
		return fmt.Errorf("registration of %s as a rival of %s failed: %s's conflicts cache not found", enrollee, enroller, enroller)
	}
	if enrolleeData.vni != c.ownerData.vni {
		return fmt.Errorf("registration of %s as a namespace rival of %s failed: VNI in %s's conflicts cache is the same as %s's VNI (%d)", enrollee, enroller, enroller, enrollee, enrolleeData.vni)
	}
	c.namespaceRivals = append(c.namespaceRivals, enrollee)
	return nil
}

func (v *Validator) recordCIDRConflict(enroller, enrollee k8stypes.NamespacedName, enrolleeData *subnetData) error {
	v.conflictsMutex.Lock()
	defer v.conflictsMutex.Unlock()
	c := v.conflicts[enroller]
	if c == nil {
		return fmt.Errorf("registration of %s as a rival of %s failed: %s's conflicts cache not found", enrollee, enroller, enroller)
	}
	if !enrolleeData.overlaps(c.ownerData) {
		return fmt.Errorf("registration of %s as a CIDR rival of %s failed: %s's CIDR in conflicts cache does not overlap with %s's CIDR", enrollee, enroller, enroller, enrollee)
	}
	c.cidrRivals = append(c.cidrRivals, enrollee)
	return nil
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

type subnetData struct {
	vni, baseU, lastU uint32
}

func parseSubnet(s *netv1a1.Subnet) (*subnetData, error) {
	_, ipNet, err := net.ParseCIDR(s.Spec.IPv4)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %#+v: %s", s, err.Error())
	}
	sd := &subnetData{}
	sd.baseU = ipv4ToUint32(ipNet.IP)
	ones, bits := ipNet.Mask.Size()
	delta := uint32(uint64(1)<<uint(bits-ones) - 1)
	sd.lastU = sd.baseU + delta
	sd.vni = s.Spec.VNI
	return sd, nil
}

func (s1 *subnetData) overlaps(s2 *subnetData) bool {
	return s1.vni == s2.vni && s1.baseU <= s2.lastU && s1.lastU >= s2.baseU
}

func (s1 *subnetData) contains(s2 *subnetData) bool {
	return s1.vni == s2.vni && s1.baseU <= s2.baseU && s1.lastU >= s2.lastU
}

func ipv4ToUint32(ip net.IP) uint32 {
	v4 := ip.To4()
	return uint32(v4[0])<<24 + uint32(v4[1])<<16 + uint32(v4[2])<<8 + uint32(v4[3])
}
