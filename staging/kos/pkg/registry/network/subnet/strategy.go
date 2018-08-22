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
	"context"
	"fmt"
	"net"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"

	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/examples/staging/kos/pkg/apis/network"
	informers "k8s.io/examples/staging/kos/pkg/client/informers/internalversion"
	listers "k8s.io/examples/staging/kos/pkg/client/listers/network/internalversion"
)

// NewStrategy creates a subnetStrategy instance and returns a pointer to it.
func NewStrategy(typer runtime.ObjectTyper, listerFactory informers.SharedInformerFactory) *subnetStrategy {
	subnetLister := listerFactory.Network().InternalVersion().Subnets().Lister()
	return &subnetStrategy{typer,
		names.SimpleNameGenerator,
		subnetLister}
}

// GetAttrs returns labels.Set, fields.Set, the presence of Initializers if any
// and error in case the given runtime.Object is not a Subnet.
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, bool, error) {
	subnet, ok := obj.(*network.Subnet)
	if !ok {
		return nil, nil, false, fmt.Errorf("given object is not a Subnet")
	}
	return labels.Set(subnet.ObjectMeta.Labels), SelectableFields(subnet), subnet.Initializers != nil, nil
}

// MatchSubnet is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific
// labels/fields.
func MatchSubnet(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *network.Subnet) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type subnetStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
	subnetLister listers.SubnetLister
}

var _ rest.RESTCreateStrategy = &subnetStrategy{}
var _ rest.RESTUpdateStrategy = &subnetStrategy{}
var _ rest.RESTDeleteStrategy = &subnetStrategy{}

func (*subnetStrategy) NamespaceScoped() bool {
	return true
}

func (*subnetStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	subnet := obj.(*network.Subnet)
	subnet.Status = network.SubnetStatus{}
}

func (*subnetStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (s *subnetStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	var errs field.ErrorList

	thisSubnet := obj.(*network.Subnet)

	vniRangeErrs := s.checkVNIRange(thisSubnet)
	errs = append(errs, vniRangeErrs...)

	cidrFormatErrs := s.checkCIDRFormat(thisSubnet)
	errs = append(errs, cidrFormatErrs...)

	if len(vniRangeErrs) > 0 {
		// If we are here, the VNI range of the created subnet is invalid. All
		// of the next validation steps make sense so long as the vni is valid,
		// thus we return immediately.
		return errs
	}

	// We need to check the under-validation subnet against subnets with the
	// same VNI only. Other subnets can be ignored.
	allSubnetsWithSameVNI, err := s.allSubnetsWithVNI(thisSubnet.Spec.VNI)
	if err != nil {
		// If we're here it was not possible to fetch the subnets with the same
		// VNI as the one under creation, thus validation cannot proceed and we
		// return immediately.
		return append(errs, field.InternalError(nil, fmt.Errorf("could not fetch subnets with VNI %d, try again later", thisSubnet.Spec.VNI)))
	}

	// TODO find a better alternative, this check is not correct. Two subnets in
	// different namespaces with the same VNI could be successfully created if
	// their creation is concurrent.
	errs = append(errs, s.checkSameVNISameNs(thisSubnet, allSubnetsWithSameVNI)...)

	if len(cidrFormatErrs) == 0 {
		// TODO find a better alternative, this check is not correct. Two
		// subnets with overlapping CIDRs could be successfully created if their
		// creation is concurrent.
		errs = append(errs, s.checkCIDRDoesNotOverlap(thisSubnet, allSubnetsWithSameVNI)...)
	}

	return errs
}

func (*subnetStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (*subnetStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (*subnetStrategy) Canonicalize(obj runtime.Object) {
}

func (s *subnetStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return s.Validate(ctx, obj)
}

const (
	minVNI uint32 = 1
	maxVNI uint32 = 2097151
)

func (*subnetStrategy) checkVNIRange(subnet *network.Subnet) field.ErrorList {
	vni := subnet.Spec.VNI
	if vni < minVNI || vni > maxVNI {
		return field.ErrorList{
			field.Invalid(field.NewPath("spec", "vni"), fmt.Sprintf("%d", vni), fmt.Sprintf("must be in the range [%d,%d]", minVNI, maxVNI)),
		}
	}
	return nil
}

func (*subnetStrategy) checkCIDRFormat(subnet *network.Subnet) field.ErrorList {
	cidr := subnet.Spec.IPv4
	if _, _, err := net.ParseCIDR(cidr); err != nil {
		return field.ErrorList{
			field.Invalid(field.NewPath("spec", "ipv4"), cidr, err.Error()),
		}
	}
	return nil
}

func (s *subnetStrategy) allSubnetsWithVNI(vni uint32) ([]*network.Subnet, error) {
	allSubnets, err := s.subnetLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	var allSubnetsWithSameVNI []*network.Subnet
	for _, aSubnet := range allSubnets {
		if aSubnet.Spec.VNI == vni {
			allSubnetsWithSameVNI = append(allSubnetsWithSameVNI, aSubnet)
		}
	}
	return allSubnetsWithSameVNI, nil
}

func (s *subnetStrategy) checkSameVNISameNs(thisSubnet *network.Subnet, allSubnetsWithSameVNI []*network.Subnet) field.ErrorList {
	errs := field.ErrorList{}

	// TODO currently thisSubnet is checked against all the subnets with the
	// same VNI. This is a sloppy attempt to deal with the fact that two subnets
	// with same VNI but different namespaces created concurrently could both be
	// created successfully. By checking all the subnets with the same VNI
	// everytime a new subnet is created, we detect such anomalies. This is
	// sloppy because if no new subnet with the given VNI is created anomalies
	// are not detected. Also, the way the cluster is notified of the anomaly is
	// through failure of the new subnet creation. But the anomaly is a general
	// one concerning all the subnets with the given VNI. Creating an event
	// could be more appropriate. When (if) we'll switch to a correct
	// enforcement of the same VNI/same Namespace rule, checking thisSubnet
	// against any of the already existing subnets with the same VNI, if there
	// are any, will be enough (and more efficient).
	for _, aSubnet := range allSubnetsWithSameVNI {
		if aSubnet.Namespace != thisSubnet.Namespace {
			errMsg := fmt.Sprintf("subnet %s/%s has the same VNI but different namespace with respect to subnet %s/%s. "+
				"Subnets with the same VNI MUST reside in the same namespace",
				thisSubnet.Namespace,
				thisSubnet.Name,
				aSubnet.Namespace,
				aSubnet.Name)
			errs = append(errs, field.Forbidden(field.NewPath("metadata", "namespace"), errMsg))
		}
	}

	return errs
}

func (s *subnetStrategy) checkCIDRDoesNotOverlap(thisSubnet *network.Subnet, allSubnetsWithSameVNI []*network.Subnet) field.ErrorList {
	errs := field.ErrorList{}

	// No need to check if parse was successful, as the check has been already
	// preformed in method checkCIDRFormat.
	thisSubnetParsed := parseSubnet(thisSubnet)

	for _, aSubnet := range allSubnetsWithSameVNI {
		if thisSubnet.UID == aSubnet.UID {
			// Do not compare the subnet under validation against itself (needed
			// in case of updates).
			continue
		}
		// No need to check if parse was successuful, as aSubnet has already
		// been created. If it couldn't be parsed its creation would have
		// failed.
		aSubnetParsed := parseSubnet(aSubnet)
		if thisSubnetParsed.overlaps(aSubnetParsed) {
			errMsg := fmt.Sprintf("subnet %s/%s IPv4 range (%s) overlaps with that of subnet %s/%s (%s). IPv4 ranges "+
				"for subnets with the same VNI MUST be disjoint",
				thisSubnet.Namespace,
				thisSubnet.Name,
				thisSubnet.Spec.IPv4,
				aSubnet.Namespace,
				aSubnet.Name,
				aSubnet.Spec.IPv4)
			errs = append(errs, field.Forbidden(field.NewPath("spec", "ipv4"), errMsg))
		}
	}

	return errs
}

type parsedSubnet struct {
	baseU, lastU uint32
}

func parseSubnet(subnet *network.Subnet) (ps parsedSubnet) {
	_, ipNet, err := net.ParseCIDR(subnet.Spec.IPv4)
	if err != nil {
		return
	}
	ps.baseU = ipv4ToUint32(ipNet.IP)
	ones, bits := ipNet.Mask.Size()
	delta := uint32(uint64(1)<<uint(bits-ones) - 1)
	ps.lastU = ps.baseU + delta
	return
}

func ipv4ToUint32(ip net.IP) uint32 {
	v4 := ip.To4()
	return uint32(v4[0])<<24 + uint32(v4[1])<<16 + uint32(v4[2])<<8 + uint32(v4[3])
}

func (ps1 parsedSubnet) overlaps(ps2 parsedSubnet) bool {
	return ps1.baseU <= ps2.lastU && ps1.lastU >= ps2.baseU
}
