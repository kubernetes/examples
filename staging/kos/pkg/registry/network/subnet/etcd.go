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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/examples/staging/kos/pkg/apis/network"
	informers "k8s.io/examples/staging/kos/pkg/client/informers/internalversion/network/internalversion"
	"k8s.io/examples/staging/kos/pkg/registry"
)

// NewREST returns a RESTStorage object that will work against API services.
func NewREST(scheme *runtime.Scheme,
	optsGetter generic.RESTOptionsGetter,
	checkConflicts bool,
	subnetInformer informers.SubnetInformer) (*registry.REST, error) {

	strategy := NewStrategy(scheme, checkConflicts, subnetInformer.Informer())

	store := &genericregistry.Store{
		NewFunc:                  func() runtime.Object { return &network.Subnet{} },
		NewListFunc:              func() runtime.Object { return &network.SubnetList{} },
		PredicateFunc:            MatchSubnet,
		DefaultQualifiedResource: network.Resource("subnets"),

		CreateStrategy: strategy,
		UpdateStrategy: strategy,
		DeleteStrategy: strategy,
	}
	options := &generic.StoreOptions{RESTOptions: optsGetter, AttrFunc: GetAttrs}
	if err := store.CompleteWithOptions(options); err != nil {
		return nil, err
	}
	return &registry.REST{store}, nil
}
