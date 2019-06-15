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

package networkattachment

import (
	"context"
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"

	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/examples/staging/kos/pkg/apis/network"
)

// NewStrategy creates and returns a networkattachmentStrategy instance.
func NewStrategy(typer runtime.ObjectTyper) networkattachmentStrategy {
	return networkattachmentStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, the presence of Initializers if any
// and error in case the given runtime.Object is not a NetworkAttachment.
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, bool, error) {
	networkattachment, ok := obj.(*network.NetworkAttachment)
	if !ok {
		return nil, nil, false, fmt.Errorf("given object is not a NetworkAttachment")
	}
	return labels.Set(networkattachment.ObjectMeta.Labels), SelectableFields(networkattachment), networkattachment.Initializers != nil, nil
}

// MatchNetworkAttachment is the filter used by the generic etcd backend to
// watch events from etcd to clients of the apiserver only interested in
// specific labels/fields.
func MatchNetworkAttachment(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *network.NetworkAttachment) fields.Set {
	return generic.AddObjectMetaFieldsSet(
		fields.Set{
			"spec.node":         obj.Spec.Node,
			"spec.subnet":       obj.Spec.Subnet,
			"status.ipv4":       obj.Status.IPv4,
			"status.hostIP":     obj.Status.HostIP,
			"status.addressVNI": strconv.FormatUint(uint64(obj.Status.AddressVNI), 10),
		},
		&obj.ObjectMeta, true)
}

type networkattachmentStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

var _ rest.RESTCreateStrategy = networkattachmentStrategy{}
var _ rest.RESTUpdateStrategy = networkattachmentStrategy{}
var _ rest.RESTDeleteStrategy = networkattachmentStrategy{}

func (networkattachmentStrategy) NamespaceScoped() bool {
	return true
}

func (networkattachmentStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	na := obj.(*network.NetworkAttachment)
	na.Status = network.NetworkAttachmentStatus{}
}

func (networkattachmentStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (networkattachmentStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

func (networkattachmentStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (networkattachmentStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (networkattachmentStrategy) Canonicalize(obj runtime.Object) {
}

func (networkattachmentStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	var errs field.ErrorList
	immutableFieldMsg := "attempt to update immutable field"
	newNa, oldNa := obj.(*network.NetworkAttachment), old.(*network.NetworkAttachment)
	if newNa.Spec.Node != oldNa.Spec.Node {
		errs = append(errs, field.Forbidden(field.NewPath("spec.node"), immutableFieldMsg))
	}
	if newNa.Spec.Subnet != oldNa.Spec.Subnet {
		errs = append(errs, field.Forbidden(field.NewPath("spec.subnet"), immutableFieldMsg))
	}
	if differ(newNa.Spec.PostCreateExec, oldNa.Spec.PostCreateExec) {
		errs = append(errs, field.Forbidden(field.NewPath("spec.postCreateExec"), immutableFieldMsg))
	}
	return errs
}

func differ(s1, s2 []string) bool {
	if len(s1) != len(s2) {
		return true
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			return true
		}
	}
	return false
}
