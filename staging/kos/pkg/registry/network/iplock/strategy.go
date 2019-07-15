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

package iplock

import (
	"context"
	"fmt"

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

// NewStrategy creates and returns a iplockStrategy instance.
func NewStrategy(typer runtime.ObjectTyper) iplockStrategy {
	return iplockStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set,
// and error in case the given runtime.Object is not a IPLock.
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	iplock, ok := obj.(*network.IPLock)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a IPLock")
	}
	return labels.Set(iplock.ObjectMeta.Labels), SelectableFields(iplock), nil
}

// MatchIPLock is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific
// labels/fields.
func MatchIPLock(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *network.IPLock) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type iplockStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

var _ rest.RESTCreateStrategy = iplockStrategy{}
var _ rest.RESTUpdateStrategy = iplockStrategy{}
var _ rest.RESTDeleteStrategy = iplockStrategy{}

func (iplockStrategy) NamespaceScoped() bool {
	return true
}

func (iplockStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (iplockStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (iplockStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

func (iplockStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (iplockStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (iplockStrategy) Canonicalize(obj runtime.Object) {
}

func (iplockStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}
