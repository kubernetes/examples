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

package connectionagent

import (
	"fmt"
	gonet "net"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"

	netv1a1 "k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
	netfabric "k8s.io/examples/staging/kos/pkg/networkfabric"
	"k8s.io/examples/staging/kos/pkg/util/parse"
)

// yes, there's a cap to the total number of local network interfaces that can
// be generated over time. uint64 is large enough though. Assuming 1Ki local
// network interfaces per second are created, the cap is reached approximately
// after 571 million years. We wish KOS all the best but we doubt it can last
// that long.
var localIfcIDGenerator uint64

type networkInterface interface {
	canBeOwnedBy(*netv1a1.NetworkAttachment) bool
	String() string
}

type localNetworkInterface struct {
	netfabric.LocalNetIfc
	hostName       string
	id             string
	postDeleteExec []string
}

var _ networkInterface = &localNetworkInterface{}

func (ifc *localNetworkInterface) canBeOwnedBy(att *netv1a1.NetworkAttachment) bool {
	return att != nil &&
		ifc.VNI == att.Status.AddressVNI &&
		ifc.GuestIP.Equal(gonet.ParseIP(att.Status.IPv4)) &&
		ifc.hostName == att.Spec.Node
}

func (ifc *localNetworkInterface) String() string {
	return fmt.Sprintf("{type=local, VNI=%#x, guestIP=%s, guestMAC=%s, name=%s}", ifc.VNI, ifc.GuestIP, ifc.GuestMAC, ifc.Name)
}

type remoteNetworkInterface struct {
	netfabric.RemoteNetIfc
}

var _ networkInterface = &remoteNetworkInterface{}

func (ifc *remoteNetworkInterface) canBeOwnedBy(att *netv1a1.NetworkAttachment) bool {
	return att != nil &&
		ifc.VNI == att.Status.AddressVNI &&
		ifc.GuestIP.Equal(gonet.ParseIP(att.Status.IPv4)) &&
		ifc.HostIP.Equal(gonet.ParseIP(att.Status.HostIP))
}

func (ifc *remoteNetworkInterface) String() string {
	return fmt.Sprintf("{type=remote, VNI=%#x, guestIP=%s, guestMAC=%s, hostIP=%s}", ifc.VNI, ifc.GuestIP, ifc.GuestMAC, ifc.HostIP)
}

func (ca *ConnectionAgent) createNetworkInterface(att *netv1a1.NetworkAttachment) (ifc networkInterface, statusErrs sliceOfString, err error) {
	if ca.node == att.Spec.Node {
		ifc, err = ca.createLocalNetworkInterface(att)
	} else {
		ifc, err = ca.createRemoteNetworkInterface(att)
	}

	if err == nil {
		attNSN := parse.AttNSN(att)
		klog.V(5).Infof("Created network interface %s for attachment %s", ifc, attNSN)
		ca.assignNetworkInterface(attNSN, ifc)
		statusErrs = ca.launchCommand(attNSN, ifc, att.Spec.PostCreateExec, "postCreate", true, true)
	}

	return
}

func (ca *ConnectionAgent) createLocalNetworkInterface(att *netv1a1.NetworkAttachment) (*localNetworkInterface, error) {
	ifc := ca.newLocalNetworkInterfaceForAttachment(att)

	tBefore := time.Now()
	err := ca.netFabric.CreateLocalIfc(ifc.LocalNetIfc)
	tAfter := time.Now()

	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "CreateLocalIfc", "err": formatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())

	return ifc, err
}

func (ca *ConnectionAgent) createRemoteNetworkInterface(att *netv1a1.NetworkAttachment) (*remoteNetworkInterface, error) {
	ifc := ca.newRemoteNetworkInterfaceForAttachment(att)

	tBefore := time.Now()
	err := ca.netFabric.CreateRemoteIfc(ifc.RemoteNetIfc)
	tAfter := time.Now()

	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "CreateRemoteIfc", "err": formatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())

	return ifc, err
}

func (ca *ConnectionAgent) deleteNetworkInterface(ownerNSN k8stypes.NamespacedName, ifcOpaque networkInterface) (err error) {
	switch ifc := ifcOpaque.(type) {
	case *localNetworkInterface:
		err = ca.deleteLocalNetworkInterface(ownerNSN, ifc)
	case *remoteNetworkInterface:
		err = ca.deleteRemoteNetworkInterface(ifc)
	default:
		err = fmt.Errorf("deleteNetworkInterface received an argument of type %T. This should never happen, only supported types are %T and %T", ifcOpaque, &localNetworkInterface{}, &remoteNetworkInterface{})
	}

	if err == nil {
		klog.V(5).Infof("Deleted network interface %s for attachment %s", ifcOpaque, ownerNSN)
		ca.unassignNetworkInterface(ownerNSN)
	}

	return
}

func (ca *ConnectionAgent) deleteLocalNetworkInterface(ownerNSN k8stypes.NamespacedName, ifc *localNetworkInterface) error {
	tBefore := time.Now()
	err := ca.netFabric.DeleteLocalIfc(ifc.LocalNetIfc)
	tAfter := time.Now()

	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "DeleteLocalIfc", "err": formatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())

	if err == nil {
		ca.launchCommand(ownerNSN, ifc, ifc.postDeleteExec, "postDelete", true, false)
	}

	return err
}

func (ca *ConnectionAgent) deleteRemoteNetworkInterface(ifc *remoteNetworkInterface) error {
	tBefore := time.Now()
	err := ca.netFabric.DeleteRemoteIfc(ifc.RemoteNetIfc)
	tAfter := time.Now()

	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "DeleteRemoteIfc", "err": formatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())

	return err
}

func (ca *ConnectionAgent) newLocalNetworkInterfaceForAttachment(att *netv1a1.NetworkAttachment) *localNetworkInterface {
	ifc := &localNetworkInterface{}
	ifc.VNI = att.Status.AddressVNI
	ifc.GuestIP = gonet.ParseIP(att.Status.IPv4)
	ifc.GuestMAC = generateMACAddr(ifc.VNI, ifc.GuestIP)
	ifc.Name = generateIfcName(ifc.GuestMAC)
	ifc.hostName = ca.node
	ifc.id = string(atomic.AddUint64(&localIfcIDGenerator, 1))
	ifc.postDeleteExec = att.Spec.PostDeleteExec
	return ifc
}

func (ca *ConnectionAgent) newRemoteNetworkInterfaceForAttachment(att *netv1a1.NetworkAttachment) *remoteNetworkInterface {
	ifc := &remoteNetworkInterface{}
	ifc.VNI = att.Status.AddressVNI
	ifc.GuestIP = gonet.ParseIP(att.Status.IPv4)
	ifc.HostIP = gonet.ParseIP(att.Status.HostIP)
	ifc.GuestMAC = generateMACAddr(ifc.VNI, ifc.GuestIP)
	return ifc
}
