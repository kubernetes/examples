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
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/klog"

	netv1a1 "k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
	netfabric "k8s.io/examples/staging/kos/pkg/networkfabric"
)

var localIfcIDGenerator uint64

// networkInterface provides access to the local networking state of
// NetworkAttachments.
type networkInterface interface {
	delete(*ConnectionAgent) error

	// getOwner returns a NetworkAttachment eligible to own the network
	// interface, if one exists. Invoke only BEFORE workers are started, when
	// only the main goroutine is running, because implementers access
	// mutex-protected state without holding the mutex.
	getOwner(*ConnectionAgent) (*netv1a1.NetworkAttachment, error)
	canBeOwnedBy(*netv1a1.NetworkAttachment, *ConnectionAgent) bool
	String() string
}

// localNetworkInterface wraps a network fabric LocalNetIfc and adds to it state
// that the network fabric ignores but is relevant to the connection agent.
type localNetworkInterface struct {
	netfabric.LocalNetIfc
	id             string
	postDeleteExec []string
}

var _ networkInterface = &localNetworkInterface{}

func (ifc *localNetworkInterface) delete(ca *ConnectionAgent) error {
	tBefore := time.Now()
	err := ca.netFabric.DeleteLocalIfc(ifc.LocalNetIfc)
	tAfter := time.Now()

	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "DeleteLocalIfc", "err": formatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
	if err == nil {
		ca.localAttachmentsGauge.Dec()
	}

	return err
}

func (ifc *localNetworkInterface) getOwner(ca *ConnectionAgent) (*netv1a1.NetworkAttachment, error) {
	indexer := ca.localAttsInformer.GetIndexer()
	vniAndIP := strconv.FormatUint(uint64(ifc.VNI), 16) + "/" + ifc.GuestIP.String()
	ownerObjs, err := indexer.ByIndex(ifcOwnerDataIndexerName, vniAndIP)
	if err != nil {
		return nil, fmt.Errorf("ByIndex(%s, %s) failed: %s", ifcOwnerDataIndexerName, vniAndIP, err.Error())
	}
	if len(ownerObjs) == 1 {
		owner := ownerObjs[0].(*netv1a1.NetworkAttachment)
		return owner, nil
	}
	return nil, nil
}

func (ifc *localNetworkInterface) canBeOwnedBy(att *netv1a1.NetworkAttachment, ca *ConnectionAgent) bool {
	return att != nil &&
		ifc.VNI == att.Status.AddressVNI &&
		ifc.GuestIP.Equal(gonet.ParseIP(att.Status.IPv4)) &&
		ca.node == att.Spec.Node
}

func (ifc *localNetworkInterface) String() string {
	return fmt.Sprintf("{type=local, VNI=%#x, guestIP=%s, guestMAC=%s, name=%s}", ifc.VNI, ifc.GuestIP, ifc.GuestMAC, ifc.Name)
}

type remoteNetworkInterface struct {
	netfabric.RemoteNetIfc
}

var _ networkInterface = &remoteNetworkInterface{}

func (ifc *remoteNetworkInterface) delete(ca *ConnectionAgent) error {
	tBefore := time.Now()
	err := ca.netFabric.DeleteRemoteIfc(ifc.RemoteNetIfc)
	tAfter := time.Now()

	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "DeleteRemoteIfc", "err": formatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
	if err == nil {
		ca.remoteAttachmentsGauge.Dec()
	}

	return err
}

func (ifc *remoteNetworkInterface) getOwner(ca *ConnectionAgent) (*netv1a1.NetworkAttachment, error) {
	indexer := ca.getRemoteAttsIndexer(ifc.VNI)
	if indexer == nil {
		return nil, nil
	}
	hostIPAndIP := ifc.HostIP.String() + "/" + ifc.GuestIP.String()
	ownerObjs, err := indexer.ByIndex(ifcOwnerDataIndexerName, hostIPAndIP)
	if err != nil {
		return nil, fmt.Errorf("ByIndex(%s, %s) failed: %s", ifcOwnerDataIndexerName, hostIPAndIP, err.Error())
	}
	if len(ownerObjs) == 1 {
		owner := ownerObjs[0].(*netv1a1.NetworkAttachment)
		return owner, nil
	}
	return nil, nil
}

func (ifc *remoteNetworkInterface) canBeOwnedBy(att *netv1a1.NetworkAttachment, _ *ConnectionAgent) bool {
	return att != nil &&
		ifc.VNI == att.Status.AddressVNI &&
		ifc.GuestIP.Equal(gonet.ParseIP(att.Status.IPv4)) &&
		ifc.HostIP.Equal(gonet.ParseIP(att.Status.HostIP))
}

func (ifc *remoteNetworkInterface) String() string {
	return fmt.Sprintf("{type=remote, VNI=%#x, guestIP=%s, guestMAC=%s, hostIP=%s}", ifc.VNI, ifc.GuestIP, ifc.GuestMAC, ifc.HostIP)
}

func (ca *ConnectionAgent) createNetworkInterface(att *netv1a1.NetworkAttachment) (networkInterface, error) {
	if ca.node == att.Spec.Node {
		return ca.createLocalNetworkInterface(att)
	}
	return ca.createRemoteNetworkInterface(att)
}

func (ca *ConnectionAgent) createLocalNetworkInterface(att *netv1a1.NetworkAttachment) (*localNetworkInterface, error) {
	ifc := ca.newLocalNetworkInterfaceForAttachment(att)

	tBefore := time.Now()
	err := ca.netFabric.CreateLocalIfc(ifc.LocalNetIfc)
	tAfter := time.Now()

	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "CreateLocalIfc", "err": formatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
	if err == nil {
		if att.Status.IfcName == "" {
			ca.attachmentCreateToLocalIfcHistogram.Observe(tAfter.Truncate(time.Second).Sub(att.CreationTimestamp.Time).Seconds())
		}
		ca.localAttachmentsGauge.Inc()
	}

	return ifc, err
}

func (ca *ConnectionAgent) createRemoteNetworkInterface(att *netv1a1.NetworkAttachment) (*remoteNetworkInterface, error) {
	ifc := ca.newRemoteNetworkInterfaceForAttachment(att)

	tBefore := time.Now()
	err := ca.netFabric.CreateRemoteIfc(ifc.RemoteNetIfc)
	tAfter := time.Now()

	ca.fabricLatencyHistograms.With(prometheus.Labels{"op": "CreateRemoteIfc", "err": formatErrVal(err != nil)}).Observe(tAfter.Sub(tBefore).Seconds())
	if err == nil {
		ca.attachmentCreateToRemoteIfcHistogram.Observe(tAfter.Truncate(time.Second).Sub(att.CreationTimestamp.Time).Seconds())
		ca.remoteAttachmentsGauge.Inc()
	}

	return ifc, err
}

func (ca *ConnectionAgent) newLocalNetworkInterfaceForAttachment(att *netv1a1.NetworkAttachment) *localNetworkInterface {
	ifc := &localNetworkInterface{}
	ifc.VNI = att.Status.AddressVNI
	ifc.GuestIP = gonet.ParseIP(att.Status.IPv4)
	ifc.GuestMAC = generateMACAddr(ifc.VNI, ifc.GuestIP)
	ifc.Name = generateIfcName(ifc.GuestMAC)
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

func (ca *ConnectionAgent) listPreExistingNetworkInterfaces() ([]networkInterface, error) {
	localInterfaces, err := ca.netFabric.ListLocalIfcs()
	if err != nil {
		return nil, fmt.Errorf("failed to list local network interfaces: %s", err.Error())
	}
	klog.V(2).Infof("Found %d pre-existing local network interfaces.", len(localInterfaces))
	ca.localAttachmentsGauge.Set(float64(len(localInterfaces)))

	remoteInterfaces, err := ca.netFabric.ListRemoteIfcs()
	if err != nil {
		return nil, fmt.Errorf("failed to list remote network interfaces: %s", err.Error())
	}
	klog.V(2).Infof("Found %d pre-existing remote network interfaces.", len(remoteInterfaces))
	ca.remoteAttachmentsGauge.Set(float64(len(remoteInterfaces)))

	networkInterfaces := make([]networkInterface, 0, len(localInterfaces)+len(remoteInterfaces))

	for _, locIfc := range localInterfaces {
		networkInterfaces = append(networkInterfaces, &localNetworkInterface{
			LocalNetIfc: locIfc,
			id:          string(atomic.AddUint64(&localIfcIDGenerator, 1))})
	}
	for _, remIfc := range remoteInterfaces {
		networkInterfaces = append(networkInterfaces, &remoteNetworkInterface{
			RemoteNetIfc: remIfc})
	}

	return networkInterfaces, nil
}
