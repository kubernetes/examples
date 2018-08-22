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

// Package logger implements a fake, NOP network fabric which does nothing but
// logging. It is useful for testing and debugging. To be able to create a
// logger network fabric in an application, you need to import package logger
// for side effects ("_" import name) in the main package of the application.
// This ensures that the factory which creates logger network fabrics is
// registered in the network fabric factory registry, and can therefore be used
// to instantiate network fabrics.
package logger

import (
	"sync"

	"github.com/golang/glog"

	"k8s.io/examples/staging/kos/pkg/networkfabric"
	"k8s.io/examples/staging/kos/pkg/networkfabric/factory"
)

const name = "logger"

// Logger is a fake network interface fabric useful for debugging/testing. It
// does nothing but logging.
type logger struct {
	localIfcsMutex sync.RWMutex
	localIfcs      map[string]networkfabric.LocalNetIfc

	remoteIfcsMutex sync.RWMutex
	remoteIfcs      map[string]networkfabric.RemoteNetIfc
}

func init() {
	// register the logger network fabric factory in the network fabric factory
	// registry, so that networkfabric pkg clients can instantiate network
	// fabrics of type logger.
	factory.RegisterFactory(func() (networkfabric.Interface, error) {
		return &logger{
			localIfcs:  make(map[string]networkfabric.LocalNetIfc),
			remoteIfcs: make(map[string]networkfabric.RemoteNetIfc),
		}, nil
	}, name)
}

func (l *logger) Name() string {
	return name
}

func (l *logger) CreateLocalIfc(ifc networkfabric.LocalNetIfc) error {
	l.localIfcsMutex.Lock()
	l.localIfcs[ifc.Name] = ifc
	l.localIfcsMutex.Unlock()
	glog.Infof("Created local interface %#+v", ifc)
	return nil
}

func (l *logger) DeleteLocalIfc(ifc networkfabric.LocalNetIfc) error {
	l.localIfcsMutex.Lock()
	delete(l.localIfcs, ifc.Name)
	l.localIfcsMutex.Unlock()
	glog.Infof("Deleted local interface %#+v", ifc)
	return nil
}

func (l *logger) CreateRemoteIfc(ifc networkfabric.RemoteNetIfc) error {
	l.remoteIfcsMutex.Lock()
	l.remoteIfcs[ifc.GuestMAC.String()] = ifc
	l.remoteIfcsMutex.Unlock()
	glog.Infof("Created remote interface %#+v", ifc)
	return nil
}

func (l *logger) DeleteRemoteIfc(ifc networkfabric.RemoteNetIfc) error {
	l.remoteIfcsMutex.Lock()
	delete(l.remoteIfcs, ifc.GuestMAC.String())
	l.remoteIfcsMutex.Unlock()
	glog.Infof("Deleted remote interface %#+v", ifc)
	return nil
}

func (l *logger) ListLocalIfcs() ([]networkfabric.LocalNetIfc, error) {
	l.localIfcsMutex.RLock()
	defer l.localIfcsMutex.RUnlock()
	localIfcsList := make([]networkfabric.LocalNetIfc, 0, len(l.localIfcs))
	for _, ifc := range l.localIfcs {
		localIfcsList = append(localIfcsList, ifc)
	}
	return localIfcsList, nil
}

func (l *logger) ListRemoteIfcs() ([]networkfabric.RemoteNetIfc, error) {
	l.remoteIfcsMutex.RLock()
	defer l.remoteIfcsMutex.RUnlock()
	remoteIfcsList := make([]networkfabric.RemoteNetIfc, 0, len(l.remoteIfcs))
	for _, ifc := range l.remoteIfcs {
		remoteIfcsList = append(remoteIfcsList, ifc)
	}
	return remoteIfcsList, nil
}
