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
	"errors"
	"fmt"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"

	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/examples/staging/kos/pkg/apis/network"
	"k8s.io/examples/staging/kos/pkg/util/parse/network/subnet"
)

const subnetVNIIndex = "subnetVNI"

// NewStrategy creates a subnetStrategy instance and returns a pointer to it.
func NewStrategy(typer runtime.ObjectTyper, checkConflicts bool, subnetInformer cache.SharedIndexInformer) *subnetStrategy {
	subnetInformer.AddIndexers(map[string]cache.IndexFunc{subnetVNIIndex: SubnetVNI})
	subnetIndexer := subnetInformer.GetIndexer()
	return &subnetStrategy{typer,
		names.SimpleNameGenerator,
		checkConflicts,
		subnetIndexer}
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
	return generic.AddObjectMetaFieldsSet(
		fields.Set{
			"spec.vni": fmt.Sprint(obj.Spec.VNI),
		},
		&obj.ObjectMeta, true)
}

type subnetStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
	checkConflicts bool
	subnetIndexer  cache.Indexer
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
		// The fields that affect a subnet validation are VNI and CIDR. If one
		// of these fields is updated, the subnet validity should be assessed
		// again, hence we update accordingly all the subnet's status fields
		// related to validation.
		newS.Status.Validated = false
		newS.Status.Errors.Validation = make([]string, 0)
	}
}

func (ss *subnetStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	s := obj.(*network.Subnet)
	return ss.validate(s)
}

func (*subnetStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (*subnetStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (*subnetStrategy) Canonicalize(obj runtime.Object) {
}

func (ss *subnetStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	newS := obj.(*network.Subnet)
	return ss.validate(newS)
}

func (ss *subnetStrategy) validate(s *network.Subnet) field.ErrorList {
	subnetSummary, parsingErrs := subnet.NewSummary(s)
	var errs field.ErrorList
	var vniOutOfRange, malformedCIDR bool
	for _, e := range parsingErrs {
		if e.Reason == subnet.VNIOutOfRange {
			vniOutOfRange = true
			errs = append(errs, field.Invalid(field.NewPath("spec", "vni"), fmt.Sprintf("%d", s.Spec.VNI), e.Error()))
		}
		if e.Reason == subnet.MalformedCIDR {
			malformedCIDR = true
			errs = append(errs, field.Invalid(field.NewPath("spec", "ipv4"), s.Spec.IPv4, e.Error()))
		}
	}

	if !ss.checkConflicts || vniOutOfRange {
		// The only checks left are those for conflicts with other subnets. If
		// we're here either such checks are disabled or the VNI of the subnet
		// under validation is out of range (and the checks on conflicts make
		// sense so long as the VNI is in range). Hence we return immediately.
		return errs
	}

	// Retrieve the summaries of all the other existing subnets with the same
	// VNI so that we can check for conflicts.
	potentialRivals, err := ss.subnetsWithVNI(fmt.Sprint(subnetSummary.VNI))
	if err != nil {
		return append(errs, field.InternalError(nil, err))
	}

	// Check whether there are Namespace and CIDR conflicts with other subnets.
	for _, pr := range potentialRivals {
		if subnetSummary.SameSubnetAs(pr) {
			// Do not compare this subnet against itself.
			continue
		}
		glog.V(2).Infof("Validating %s against %s", subnetSummary.NamespacedName, pr.NamespacedName)
		if subnetSummary.NSConflict(pr) {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "vni"), fmt.Sprintf("subnets with same VNI must be within same namespace, but %s has the same VNI and a different namespace", pr.NamespacedName)))
		}
		if !malformedCIDR && subnetSummary.CIDRConflict(pr) {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "ipv4"), fmt.Sprintf("subnets with same VNI must have disjoint CIDRs, but CIDR overlaps with %s's", pr.NamespacedName)))
		}
	}

	return errs
}

func (ss *subnetStrategy) subnetsWithVNI(vni string) ([]*subnet.Summary, error) {
	subnets, err := ss.subnetIndexer.ByIndex(subnetVNIIndex, vni)
	if err != nil {
		return nil, fmt.Errorf(".ByIndex failed for index %s and vni %s: %s", subnetVNIIndex, vni, err.Error())
	}
	glog.V(3).Infof("Found %d subnets with vni %s", len(subnets), vni)

	summaries := make([]*subnet.Summary, 0, len(subnets))
	for _, s := range subnets {
		if summary, err := subnet.NewSummary(s); err == nil {
			summaries = append(summaries, summary)
			glog.V(3).Infof("Parsed subnet %s with vni %s", summary.NamespacedName, vni)
		} else {
			glog.Errorf("failed to parse subnet %s with vni %s: %s", summary.NamespacedName, vni, err.Error())
		}
	}

	return summaries, nil
}

func SubnetVNI(obj interface{}) ([]string, error) {
	s, isInternalVersionSubnet := obj.(*network.Subnet)
	if isInternalVersionSubnet {
		return []string{fmt.Sprint(s.Spec.VNI)}, nil
	}
	return nil, errors.New("received object which is not an internal version subnet")
}
