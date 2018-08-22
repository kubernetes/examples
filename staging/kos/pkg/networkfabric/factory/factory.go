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

// Package factory exports an API clients can use to get instances of network
// fabrics, by providing the name of the network fabric. It also exports an API
// network fabric implementers can use to register themselves as implementers by
// providing their network fabric factory function and the name under which they
// wish to be registered.
package factory

import (
	"fmt"
	"strings"

	"k8s.io/examples/staging/kos/pkg/networkfabric"
)

// Interface is the signature of functions that can be registered as network
// fabric factories.
type Interface func() (networkfabric.Interface, error)

// factoryRegistry associates netfabric names with the factory functions for
// those netfabric. Init with a capacity of 1 because we expect that at least
// one factory will always be registered.
var factoryRegistry = make(map[string]Interface, 1)

// RegisterFactory registers a network fabric factory under the given name.
// After a successful invocation, an instance of a network fabric created by the
// registered factory can be obtained by invoking NewNetFabricForName using the
// name used for registration as the input parameter. This function is meant to
// be used ONLY in init() functions of network fabric implementers. Invoking
// registerFabric more than once with the same name panics.
func RegisterFactory(factory Interface, name string) {
	if _, nameAlreadyInUse := factoryRegistry[name]; nameAlreadyInUse {
		panic(fmt.Sprintf("a factory with name %s is already registered. Use a different name", name))
	}
	factoryRegistry[name] = factory
}

// NewNetFabricForName returns a network fabric created by the factory
// registered under the given name. An error is returned if no such fabric is
// found.
func NewNetFabricForName(name string) (networkfabric.Interface, error) {
	newNetfabric, nameIsRegistered := factoryRegistry[name]
	if !nameIsRegistered {
		var err error
		if len(factoryRegistry) > 0 {
			registeredNames := make([]string, 0, len(factoryRegistry))
			for aRegisteredName := range factoryRegistry {
				registeredNames = append(registeredNames, aRegisteredName)
			}
			err = fmt.Errorf("No fabric is registered with name %s. Registered names are: %s", name, strings.Join(registeredNames, ","))
		} else {
			err = fmt.Errorf("No fabric is registered with name %s because there are no registered fabrics", name)
		}
		return nil, err
	}
	return newNetfabric()
}
