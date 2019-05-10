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
	newS, oldS := obj.(*network.Subnet), old.(*network.Subnet)
	if oldS.Spec.VNI != newS.Spec.VNI || oldS.Spec.IPv4 != newS.Spec.IPv4 {
		// The fields that affect a subnet usability are VNI and CIDR. If one of
		// these fields is updated, the subnet usability should be assessed
		// again, hence we update accordingly all the subnet's status fields
		// related to usability.
		newS.Status.Usable = false
		for i, c := range newS.Status.Conditions {
			if c.Type == network.SubnetConflict {
				newS.Status.Conditions = append(newS.Status.Conditions[0:i], newS.Status.Conditions[i+1:]...)
			}
		}
	}
}

func (*subnetStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	s := obj.(*network.Subnet)
	return validate(s)
}

func (*subnetStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (*subnetStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (*subnetStrategy) Canonicalize(obj runtime.Object) {
}

func (*subnetStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	newS := obj.(*network.Subnet)
	return validate(newS)
}

func validate(s *network.Subnet) field.ErrorList {
	return append(checkVNIRange(s), checkCIDRFormat(s)...)
}

const (
	minVNI uint32 = 1
	maxVNI uint32 = 2097151
)

func checkVNIRange(s *network.Subnet) field.ErrorList {
	vni := s.Spec.VNI
	if vni < minVNI || vni > maxVNI {
		return field.ErrorList{
			field.Invalid(field.NewPath("spec", "vni"), fmt.Sprintf("%d", vni), fmt.Sprintf("must be in the range [%d,%d]", minVNI, maxVNI)),
		}
	}
	return field.ErrorList{}
}

func checkCIDRFormat(s *network.Subnet) field.ErrorList {
	cidr := s.Spec.IPv4
	if _, _, err := net.ParseCIDR(cidr); err != nil {
		return field.ErrorList{
			field.Invalid(field.NewPath("spec", "ipv4"), cidr, err.Error()),
		}
	}
	return field.ErrorList{}
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
