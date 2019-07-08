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
	"strconv"

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
			"spec.vni": strconv.FormatUint(uint64(obj.Spec.VNI), 10),
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
}

func (ss *subnetStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	s := obj.(*network.Subnet)
	subnetSummary, parsingErrs := subnet.NewSummary(s)
	var errs field.ErrorList
	var vniOutOfRange, malformedCIDR bool
	for _, e := range parsingErrs {
		if e.Reason == subnet.VNIOutOfRange {
			vniOutOfRange = true
			errs = append(errs, field.Invalid(field.NewPath("spec", "vni"), strconv.FormatUint(uint64(s.Spec.VNI), 10), e.Error()))
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

	return append(errs, ss.checkNSAndCIDRConflicts(subnetSummary, malformedCIDR)...)
}

func (ss *subnetStrategy) checkNSAndCIDRConflicts(candidate *subnet.Summary, malformedCIDR bool) (errs field.ErrorList) {
	potentialRivals, err := ss.subnetIndexer.ByIndex(subnetVNIIndex, strconv.FormatUint(uint64(candidate.VNI), 10))
	if err != nil {
		glog.Errorf("subnetIndexer.ByIndex failed for index %s and vni %d: %s", subnetVNIIndex, candidate.VNI, err.Error())
		errs = field.ErrorList{field.InternalError(field.NewPath("spec", "vni"), errors.New("failed to retrieve other subnets with same vni"))}
		return
	}
	glog.V(5).Infof("Found %d subnets with vni %d", len(potentialRivals), candidate.VNI)
	// Check whether there are Namespace and CIDR conflicts with other subnets.
	for _, potentialRival := range potentialRivals {
		// Ignore the error because a malformed subnet would not have been
		// allowed: it's the code in this file that performs validation.
		pr, _ := subnet.NewSummary(potentialRival)

		glog.V(2).Infof("Validating %s against %s", candidate.NamespacedName, pr.NamespacedName)
		if candidate.NSConflict(pr) {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "vni"), fmt.Sprintf("subnets with same VNI must be within same namespace, but %s has the same VNI and a different namespace", pr.NamespacedName)))
		}
		if !malformedCIDR && candidate.CIDRConflict(pr) {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "ipv4"), fmt.Sprintf("subnets with same VNI must have disjoint CIDRs, but CIDR overlaps with %s's", pr.NamespacedName)))
		}
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

func (ss *subnetStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	var errs field.ErrorList
	immutableFieldMsg := "attempt to update immutable field"
	newS, oldS := obj.(*network.Subnet), old.(*network.Subnet)
	if newS.Spec.VNI != oldS.Spec.VNI {
		errs = append(errs, field.Forbidden(field.NewPath("spec", "vni"), immutableFieldMsg))
	}
	if newS.Spec.IPv4 != oldS.Spec.IPv4 {
		errs = append(errs, field.Forbidden(field.NewPath("spec", "ipv4"), immutableFieldMsg))
	}
	return errs
}

func SubnetVNI(obj interface{}) ([]string, error) {
	s, isInternalVersionSubnet := obj.(*network.Subnet)
	if isInternalVersionSubnet {
		return []string{strconv.FormatUint(uint64(s.Spec.VNI), 10)}, nil
	}
	return nil, errors.New("received object which is not an internal version subnet")
}
