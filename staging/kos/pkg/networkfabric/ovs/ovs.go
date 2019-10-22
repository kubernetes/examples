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

// Package ovs implements an Openvswitch based network fabric. To be able to
// create an ovs network fabric in an application, you need to import package
// ovs for side effects ("_" import name) in the main package of the
// application. This ensures that the factory which creates ovs network fabrics
// is registered in the network fabric factory registry, and can therefore be
// used to instantiate network fabrics.
package ovs

import (
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/klog"

	"k8s.io/examples/staging/kos/pkg/networkfabric"
	"k8s.io/examples/staging/kos/pkg/networkfabric/factory"
	"k8s.io/examples/staging/kos/pkg/util/convert"
)

const (
	// name of this network fabric. Used to register this fabric factory in the
	// fabric factory registry.
	name = "ovs"

	// time to wait after a failure before performing the needed clean up
	cleanupDelay = 1 * time.Second

	// string templates for regexps used to parse the OpenFlow flows
	decNbrRegexpStr = "[0-9]+"
	inPortRegexpStr = "in_port=" + decNbrRegexpStr
	outputRegexpStr = "output:" + decNbrRegexpStr
	hexNbrRegexpStr = "0[xX][0-9a-fA-F]+"
	tunIDRegexpStr  = "tun_id=" + hexNbrRegexpStr
	loadRegexpStr   = "load:" + hexNbrRegexpStr
	cookieRegexpStr = "cookie=" + hexNbrRegexpStr
	ipv4RegexpStr   = "(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)"
	arpTPARegexpStr = "arp_tpa=" + ipv4RegexpStr
	macRegexpStr    = "([0-9A-Fa-f]{2}[:-]){5}([0-9A-Fa-f]{2})"

	// the command to dump the OpenFlow flows contains some hex numbers prefixed
	// by 0x or 0X. Store 0xX in a const used to remove such leading chars before
	// parsing hex numbers
	hexPrefixChars = "0xX"

	// remoteFlowsFingerprint stores a string (the name of the tunnel
	// destination field) that all and only the flows created for remote
	// interfaces contain: use it to identify such flows
	remoteFlowsFingerprint = "NXM_NX_TUN_IPV4_DST"
	arpFlowsFingerprint    = "arp"
)

type ovsFabric struct {
	bridge                string
	vtep                  string
	vtepOFport            uint16
	flowParsingKit        *regexpKit
	lockedVNIIPPairsMutex sync.Mutex
	lockedVNIIPPairs      map[vniAndIP]struct{}
}

type regexpKit struct {
	decNbr *regexp.Regexp
	inPort *regexp.Regexp
	output *regexp.Regexp
	hexNbr *regexp.Regexp
	tunID  *regexp.Regexp
	load   *regexp.Regexp
	cookie *regexp.Regexp
	ipv4   *regexp.Regexp
	arpTPA *regexp.Regexp
	mac    *regexp.Regexp
}

type vniAndIP struct {
	vni uint32
	ip  uint32
}

func init() {
	// register the OvS network fabric factory in the network fabric factory
	// registry, so that networkfabric pkg clients can instantiate network
	// fabrics of type OvS.
	factory.RegisterFactory(newFactory("kos"), name)
}

// newFactory returns an OvS network fabric factory function whose underlying
// OvS bridge has name bridge.
func newFactory(bridge string) factory.Interface {
	// the returned function can be used to instantiate an OvS network fabric.
	return func() (networkfabric.Interface, error) {
		f := &ovsFabric{
			lockedVNIIPPairs: make(map[vniAndIP]struct{}),
		}
		f.initFlowsParsingKit()
		klog.V(6).Infof("Initialized bridge %s flows parsing kit", bridge)
		if err := f.initBridge(bridge); err != nil {
			return nil, fmt.Errorf("failed to create OvS network fabric: %s", err.Error())
		}
		klog.V(2).Infof("Initialized bridge %s", bridge)
		return f, nil
	}
}

func (f *ovsFabric) Name() string {
	return name
}

func (f *ovsFabric) CreateLocalIfc(ifc networkfabric.LocalNetIfc) (err error) {
	if err = f.lockVNIIPPair(ifc.VNI, ifc.GuestIP); err != nil {
		err = fmt.Errorf("failed to lock IP %s in VNI %#x: %s", ifc.GuestIP, ifc.VNI, err.Error())
		return
	}
	defer func() {
		if err != nil {
			f.unlockVNIIPPair(ifc.VNI, ifc.GuestIP)
			klog.V(5).Infof("Unlocked IP %s in VNI %#x", ifc.GuestIP, ifc.VNI)
		}
	}()

	if err = f.createIfc(ifc.Name, ifc.GuestMAC); err != nil {
		return
	}
	defer func() {
		// clean up executed in case retrieving the openflow port or adding the
		// flows fails. Needed to avoid leaking interfaces because there's no
		// guarantee that in case of error the client will retry to create ifc,
		// or, even if there's a retry, the ifc name might have changed
		if err != nil {
			// wait to reduce chances of another failure in case OvS is
			// experiencing transient failures
			time.Sleep(cleanupDelay)
			if cleanUpErr := f.deleteIfc(ifc.Name); cleanUpErr != nil {
				klog.Errorf("Could not delete local interface %s during clean up after failure: %s",
					ifc.Name,
					cleanUpErr.Error())
			}
		}
	}()

	ofPort, err := f.getIfcOFport(ifc.Name)
	if err != nil {
		return
	}

	if err = f.addLocalIfcFlows(ofPort, ifc.VNI, ifc.GuestMAC, ifc.GuestIP); err != nil {
		return
	}

	klog.V(2).Infof("Created local interface %#+v connected to bridge %s",
		ifc,
		f.bridge)

	return
}

func (f *ovsFabric) DeleteLocalIfc(ifc networkfabric.LocalNetIfc) error {
	ofPort, err := f.getIfcOFport(ifc.Name)
	if err != nil {
		return err
	}

	if err := f.deleteLocalIfcFlows(ofPort, ifc.VNI, ifc.GuestMAC, ifc.GuestIP); err != nil {
		return err
	}

	if err := f.deleteIfc(ifc.Name); err != nil {
		return err
	}

	f.unlockVNIIPPair(ifc.VNI, ifc.GuestIP)

	klog.V(2).Infof("Deleted local interface %#+v connected to bridge %s",
		ifc,
		f.bridge)

	return nil
}

func (f *ovsFabric) CreateRemoteIfc(ifc networkfabric.RemoteNetIfc) (err error) {
	if err = f.lockVNIIPPair(ifc.VNI, ifc.GuestIP); err != nil {
		err = fmt.Errorf("failed to lock IP %s in VNI %#x: %s", ifc.GuestIP, ifc.VNI, err.Error())
		return
	}
	defer func() {
		if err != nil {
			f.unlockVNIIPPair(ifc.VNI, ifc.GuestIP)
			klog.V(5).Infof("Unlocked IP %s in VNI %#x", ifc.GuestIP, ifc.VNI)
		}
	}()
	if err = f.addRemoteIfcFlows(ifc.VNI, ifc.GuestMAC, ifc.HostIP, ifc.GuestIP); err != nil {
		return
	}
	klog.V(2).Infof("Created remote interface %#+v", ifc)
	return
}

func (f *ovsFabric) DeleteRemoteIfc(ifc networkfabric.RemoteNetIfc) error {
	if err := f.deleteRemoteIfcFlows(ifc.VNI, ifc.GuestMAC, ifc.GuestIP); err != nil {
		return err
	}
	f.unlockVNIIPPair(ifc.VNI, ifc.GuestIP)
	klog.V(2).Infof("Deleted remote interface %#+v", ifc)
	return nil
}

func (f *ovsFabric) ListLocalIfcs() ([]networkfabric.LocalNetIfc, error) {
	// build a map from openflow port nbr to local ifc name
	ofPortToIfcName, err := f.getOFportsToLocalIfcNames()
	if err != nil {
		return nil, err
	}

	// useful flows associated with local interfaces are those for ARP and normal
	// datalink traffic. The one for tunneling is useless, it only carries the
	// VNI of an interface, which is stored in the two other flows as well
	localFlows, err := f.getUsefulLocalFlows()
	if err != nil {
		return nil, err
	}

	// build a map from the openflow port of an interface to the two useful
	// flows it is associated with
	klog.V(4).Infof("Pairing ARP and normal Datalink traffic flows of local interfaces in bridge %s...",
		f.bridge)
	ofPortToLocalFlowsPairs := f.ofPortToLocalFlowsPairs(localFlows)

	// use the map from ofPorts to pairs of flows to build a map from ofPort
	// to LocalNetIfc structs with all the fields set but the name (because no
	// flow carries info about the interface name)
	klog.V(4).Infof("Parsing flows pairs found in bridge %s into local Network Interfaces...",
		f.bridge)
	ofPortToNamelessIfc := f.parseLocalFlowsPairs(ofPortToLocalFlowsPairs)

	// assign a name to the nameless ifcs built with the previous instruction.
	// completeIfcs are those to return, incompleteIfcs are interfaces for whom
	// flows could not be found. Such interfaces are created if the connection
	// agent crashes between the creation of the interface and the addition of
	// its flows, or if the latter fails for whatever reason and also deleting
	// the interface for clean up fails.
	klog.V(4).Infof("Naming local Network Interfaces parsed out of bridge %s...",
		f.bridge)
	completeIfcs, incompleteIfcs := f.nameIfcs(ofPortToIfcName, ofPortToNamelessIfc)

	// best effort attempt to delete orphan interfaces
	klog.V(4).Infof("Deleting network devices connected to bridge %s for whom OpenFlow flows were not found...",
		f.bridge)
	f.deleteIncompleteIfcs(incompleteIfcs)

	klog.V(4).Info("Locking local network interfaces VNIs and IPs...")
	for _, aCompleteIfc := range completeIfcs {
		f.lockVNIIPPair(aCompleteIfc.VNI, aCompleteIfc.GuestIP)
	}

	return completeIfcs, nil
}

func (f *ovsFabric) ListRemoteIfcs() ([]networkfabric.RemoteNetIfc, error) {
	flows, err := f.getRemoteFlows()
	if err != nil {
		return nil, err
	}

	// each remote interface is associated with two flows. Arrange flows in pairs
	// by interface.
	klog.V(4).Infof("Pairing ARP and normal Datalink traffic flows of remote interfaces in bridge %s...",
		f.bridge)
	perIfcFlowPairs := f.pairRemoteFlowsPerIfc(flows)
	klog.V(4).Infof("Parsing flows pairs found in bridge %s into remote Network Interfaces...",
		f.bridge)

	ifcs := f.parseRemoteFlowsPairs(perIfcFlowPairs)

	klog.V(4).Info("Locking remote network interfaces VNIs and IPs...")
	for _, anIfc := range ifcs {
		f.lockVNIIPPair(anIfc.VNI, anIfc.GuestIP)
	}

	return ifcs, nil
}

func (f *ovsFabric) initFlowsParsingKit() {
	f.flowParsingKit = &regexpKit{
		decNbr: regexp.MustCompile(decNbrRegexpStr),
		inPort: regexp.MustCompile(inPortRegexpStr),
		output: regexp.MustCompile(outputRegexpStr),
		hexNbr: regexp.MustCompile(hexNbrRegexpStr),
		tunID:  regexp.MustCompile(tunIDRegexpStr),
		load:   regexp.MustCompile(loadRegexpStr),
		cookie: regexp.MustCompile(cookieRegexpStr),
		ipv4:   regexp.MustCompile(ipv4RegexpStr),
		arpTPA: regexp.MustCompile(arpTPARegexpStr),
		mac:    regexp.MustCompile(macRegexpStr),
	}
}

func (f *ovsFabric) initBridge(name string) error {
	f.bridge = name
	f.vtep = "vtep"

	if err := f.createBridge(); err != nil {
		return err
	}

	if err := f.addVTEP(); err != nil {
		return err
	}

	vtepOFport, err := f.getIfcOFport(f.vtep)
	if err != nil {
		return err
	}
	f.vtepOFport = vtepOFport

	if err := f.addDefaultFlows(); err != nil {
		return err
	}

	return nil
}

func (f *ovsFabric) createBridge() error {
	createBridge := f.newCreateBridgeCmd()

	if out, err := createBridge.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create bridge %s: %s: %s",
			f.bridge,
			err.Error(),
			string(out))
	}
	klog.V(4).Infof("Created OvS bridge %s", f.bridge)

	return nil
}

func (f *ovsFabric) addVTEP() error {
	addVTEP := f.newAddVTEPCmd()

	if out, err := addVTEP.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to add VTEP port and interface to bridge %s: %s: %s",
			f.bridge,
			err.Error(),
			string(out))
	}
	klog.V(4).Infof("Added VTEP to bridge %s", f.bridge)

	return nil
}

func (f *ovsFabric) getIfcOFport(ifc string) (uint16, error) {
	getIfcOFport := f.newGetIfcOFportCmd(ifc)

	outBytes, err := getIfcOFport.CombinedOutput()
	out := strings.TrimRight(string(outBytes), "\n")
	if err != nil {
		return 0, fmt.Errorf("failed to retrieve OpenFlow port of interface %s in bridge %s: %s: %s",
			ifc,
			f.bridge,
			err.Error(),
			out)
	}

	ofPort := parseOFport(out)
	klog.V(4).Infof("Retrieved OpenFlow port nbr (%d) of network device %s in bridge %s",
		ofPort,
		ifc,
		f.bridge)

	return ofPort, nil
}

func (f *ovsFabric) addDefaultFlows() error {
	defaultResubmitToT1Flow := "table=0,priority=1,actions=resubmit(,1)"
	defaultDropFlow := "table=1,priority=1,actions=drop"

	addFlows := f.newAddFlowsCmd(defaultResubmitToT1Flow, defaultDropFlow)

	if out, err := addFlows.CombinedOutput(); err != nil {
		return newAddFlowsErr(strings.Join([]string{defaultResubmitToT1Flow, defaultDropFlow}, " "),
			f.bridge,
			err.Error(),
			string(out))
	}

	return nil
}

func (f *ovsFabric) createIfc(ifc string, mac net.HardwareAddr) error {
	createIfc := f.newCreateIfcCmd(ifc, mac)

	if out, err := createIfc.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create local ifc %s with MAC %s and plug it into bridge %s: %s: %s",
			ifc,
			mac,
			f.bridge,
			err.Error(),
			string(out))
	}

	klog.V(4).Infof("Created network device %s with MAC %s connected to bridge %s",
		ifc,
		mac,
		f.bridge)

	return nil
}

func (f *ovsFabric) deleteIfc(ifc string) error {
	// the interface is managed by OvS (interface type internal), hence deleting
	// the bridge port it is associated with automatically deletes it
	deleteIfc := f.newDeleteBridgePortCmd(ifc)

	if out, err := deleteIfc.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to delete local ifc %s attached to bridge %s: %s: %s",
			ifc,
			f.bridge,
			err.Error(),
			string(out))
	}

	klog.V(4).Infof("Deleted network device %s connected to bridge %s",
		ifc,
		f.bridge)

	return nil
}

func (f *ovsFabric) addLocalIfcFlows(ofPort uint16, tunID uint32, dlDst net.HardwareAddr, arpTPA net.IP) error {
	tunnelingFlow := fmt.Sprintf("table=0,in_port=%d,actions=set_field:%#x->tun_id,resubmit(,1)",
		ofPort,
		tunID)
	dlTrafficFlow := fmt.Sprintf("table=1,tun_id=%#x,dl_dst=%s,actions=output:%d",
		tunID,
		dlDst,
		ofPort)
	arpFlow := fmt.Sprintf("table=1,tun_id=%#x,arp,arp_tpa=%s,actions=output:%d",
		tunID,
		arpTPA,
		ofPort)

	addFlows := f.newAddFlowsCmd(tunnelingFlow, dlTrafficFlow, arpFlow)

	if out, err := addFlows.CombinedOutput(); err != nil {
		return newAddFlowsErr(strings.Join([]string{tunnelingFlow, dlTrafficFlow, arpFlow}, " "),
			f.bridge,
			err.Error(),
			string(out))
	}

	klog.V(4).Infof("Bridge %s: added OpenFlow flows: \n\t%s\n\t%s\n\t%s",
		f.bridge,
		tunnelingFlow,
		dlTrafficFlow,
		arpFlow)

	return nil
}

func (f *ovsFabric) deleteLocalIfcFlows(ofPort uint16, tunID uint32, dlDst net.HardwareAddr, arpTPA net.IP) error {
	tunnelingFlow := fmt.Sprintf("in_port=%d", ofPort)
	dlTrafficFlow := fmt.Sprintf("tun_id=%#x,dl_dst=%s", tunID, dlDst)
	arpFlow := fmt.Sprintf("tun_id=%#x,arp,arp_tpa=%s", tunID, arpTPA)

	delFlows := f.newDelFlowsCmd(tunnelingFlow, dlTrafficFlow, arpFlow)

	if out, err := delFlows.CombinedOutput(); err != nil {
		return newDelFlowsErr(strings.TrimRight(strings.Join([]string{tunnelingFlow, dlTrafficFlow, arpFlow}, " "), " "),
			f.bridge,
			err.Error(),
			string(out))
	}

	klog.V(4).Infof("Bridge %s: deleted OpenFlow flows: \n\t%s\n\t%s\n\t%s",
		f.bridge,
		tunnelingFlow,
		dlTrafficFlow,
		arpFlow)

	return nil
}

func (f *ovsFabric) addRemoteIfcFlows(tunID uint32, dlDst net.HardwareAddr, tunDst, arpTPA net.IP) error {
	// cookies are an opaque numeric ID that OpenFlow offers to group together
	// flows. Here we use one to link together the two flows created for the
	// same remote interface. It is computed as a function of dlDst (the MAC of
	// the interface), making it impossible for two flows created out of different
	// remote interfaces to have the same one. It is needed because the essential
	// fields in the two flows created do not overlap. Without it it's
	// impossible to pair remote flows that were originated by the same interface,
	// but we need this coupling at remote interfaces list time.
	cookie := convert.MACAddressToUint64(dlDst)

	dlTrafficFlow := fmt.Sprintf("table=1,cookie=%d,tun_id=%#x,dl_dst=%s,actions=set_field:%s->tun_dst,output:%d",
		cookie,
		tunID,
		dlDst,
		tunDst,
		f.vtepOFport)
	arpFlow := fmt.Sprintf("table=1,cookie=%d,tun_id=%#x,arp,arp_tpa=%s,actions=set_field:%s->tun_dst,output:%d",
		cookie,
		tunID,
		arpTPA,
		tunDst,
		f.vtepOFport)

	addFlows := f.newAddFlowsCmd(dlTrafficFlow, arpFlow)

	if out, err := addFlows.CombinedOutput(); err != nil {
		return newAddFlowsErr(strings.Join([]string{dlTrafficFlow, arpFlow}, " "),
			f.bridge,
			err.Error(),
			string(out))
	}

	klog.V(4).Infof("Bridge %s: added OpenFlow flows: \n\t%s\n\t%s",
		f.bridge,
		dlTrafficFlow,
		arpFlow)

	return nil
}

func (f *ovsFabric) deleteRemoteIfcFlows(tunID uint32, dlDst net.HardwareAddr, arpTPA net.IP) error {
	dlTrafficFlow := fmt.Sprintf("table=1,tun_id=%#x,dl_dst=%s",
		tunID,
		dlDst)
	arpFlow := fmt.Sprintf("table=1,tun_id=%#x,arp,arp_tpa=%s",
		tunID,
		arpTPA)

	delFlows := f.newDelFlowsCmd(dlTrafficFlow, arpFlow)

	if out, err := delFlows.CombinedOutput(); err != nil {
		return newDelFlowsErr(strings.Join([]string{dlTrafficFlow, arpFlow}, " "),
			f.bridge,
			err.Error(),
			string(out))
	}

	klog.V(4).Infof("Bridge %s: deleted OpenFlow flows: \n\t%s\n\t%s",
		f.bridge,
		dlTrafficFlow,
		arpFlow)

	return nil
}

func parseOFport(ofPort string) uint16 {
	ofP, _ := strconv.ParseUint(ofPort, 10, 16)
	return uint16(ofP)
}

func (f *ovsFabric) getOFportsToLocalIfcNames() (map[uint16]string, error) {
	listOFportsAndIfcNames := f.newListOFportsAndIfcNamesCmd()

	out, err := listOFportsAndIfcNames.CombinedOutput()
	outStr := strings.TrimRight(string(out), "\n")
	if err != nil {
		return nil, fmt.Errorf("failed to list local ifcs names and ofPorts: %s: %s",
			err.Error(),
			outStr)
	}

	klog.V(4).Infof("Parsing OpenFlow ports and Interface names in bridge %s...",
		f.bridge)

	return f.parseOFportsAndIfcNames(outStr), nil
}

func (f *ovsFabric) getUsefulLocalFlows() ([]string, error) {
	flows, err := f.getFlows()
	if err != nil {
		return nil, err
	}
	return onlyUsefulLocalFlowsFunc(flows), nil
}

func (f *ovsFabric) getRemoteFlows() ([]string, error) {
	flows, err := f.getFlows()
	if err != nil {
		return nil, err
	}
	return onlyRemoteFlowsFunc(flows), nil
}

func (f *ovsFabric) ofPortToLocalFlowsPairs(flows []string) map[uint16][]string {
	ofPortToFlowsPairs := make(map[uint16][]string, len(flows)/2)
	for _, aFlow := range flows {
		ofPort := f.usefulLocalFlowOFport(aFlow)
		ofPortToFlowsPairs[ofPort] = append(ofPortToFlowsPairs[ofPort], aFlow)
		if len(ofPortToFlowsPairs[ofPort]) == 2 {
			klog.V(5).Infof("Paired flows \"%s\" \"%s\"", ofPortToFlowsPairs[ofPort][0], ofPortToFlowsPairs[ofPort][1])
		}
	}
	return ofPortToFlowsPairs
}

func (f *ovsFabric) parseLocalFlowsPairs(ofPortToPair map[uint16][]string) map[uint16]*networkfabric.LocalNetIfc {
	ofPortToIfc := make(map[uint16]*networkfabric.LocalNetIfc, len(ofPortToPair))
	for ofPort, aPair := range ofPortToPair {
		klog.V(5).Infof("Parsing flows pair \"%s\" \"%s\"...", aPair[0], aPair[1])
		ofPortToIfc[ofPort] = f.parseLocalFlowPair(aPair)
	}
	return ofPortToIfc
}

func (f *ovsFabric) pairRemoteFlowsPerIfc(flows []string) [][]string {
	flowPairs := make(map[string][]string, len(flows)/2)
	for _, aFlow := range flows {
		// two flows belong to the same pair if they have the same cookie
		cookie := f.extractCookie(aFlow)
		flowPairs[cookie] = append(flowPairs[cookie], aFlow)
		if len(flowPairs[cookie]) == 2 {
			klog.V(5).Infof("Paired flows \"%s\" \"%s\"", flowPairs[cookie][0], flowPairs[cookie][1])
		}
	}

	// we don't need a map where the key is the cookie. We only need flow pairs,
	// hence we store them in a slice of slices (each pair is stored in an
	// innermost slice)
	perIfcFlowPairs := make([][]string, 0, len(flowPairs))
	for _, aFlowPair := range flowPairs {
		perIfcFlowPairs = append(perIfcFlowPairs, aFlowPair)
	}
	return perIfcFlowPairs
}

func (f *ovsFabric) parseRemoteFlowsPairs(flowsPairs [][]string) []networkfabric.RemoteNetIfc {
	ifcs := make([]networkfabric.RemoteNetIfc, 0, len(flowsPairs))
	for _, aPair := range flowsPairs {
		klog.V(5).Infof("Parsing flows pair \"%s\" \"%s\"...", aPair[0], aPair[1])
		ifcs = append(ifcs, f.parseRemoteFlowPair(aPair))
	}
	return ifcs
}

func (f *ovsFabric) nameIfcs(ofPortToIfcName map[uint16]string, ofPortToIfc map[uint16]*networkfabric.LocalNetIfc) ([]networkfabric.LocalNetIfc, []string) {
	// we assume that most of the interfaces can be completed (both the network
	// device and the OpenFlow flows were found), that's why completeIfcs
	// has capacity len(ofPortToIfc) whereas incompleteIfcs has capacity 0
	completeIfcs := make([]networkfabric.LocalNetIfc, 0, len(ofPortToIfc))
	incompleteIfcs := make([]string, 0)

	for ofPort, name := range ofPortToIfcName {
		ifc := ofPortToIfc[ofPort]
		if ifc == nil {
			incompleteIfcs = append(incompleteIfcs, name)
			klog.V(5).Infof("No flows found for network device %s connected to bridge %s",
				name,
				f.bridge)
		} else {
			ifc.Name = name
			completeIfcs = append(completeIfcs, *ifc)
			klog.V(5).Infof("Named local network interface %#+v",
				ifc)
		}
	}

	return completeIfcs, incompleteIfcs
}

func (f *ovsFabric) deleteIncompleteIfcs(ifcs []string) {
	for _, anIfc := range ifcs {
		if err := f.deleteIfc(anIfc); err != nil {
			klog.Errorf("Failed to delete interface %s from bridge %s: %s. Deletion needed because no flows for the interface were found",
				anIfc,
				f.bridge,
				err.Error())
		} else {
			klog.V(5).Infof("Deleted interface %s from bridge %s: no flows found",
				anIfc,
				f.bridge)
		}
	}
}

func (f *ovsFabric) parseOFportsAndIfcNames(ofPortsAndIfcNamesRaw string) map[uint16]string {
	ofPortsAndIfcNames := strings.Split(ofPortsAndIfcNamesRaw, "\n")
	ofPortToIfcName := make(map[uint16]string, len(ofPortsAndIfcNames))

	for _, anOFportAndIfcNamePair := range ofPortsAndIfcNames {
		f.parseOFportAndIfcName(anOFportAndIfcNamePair, ofPortToIfcName)
	}

	return ofPortToIfcName
}

func (f *ovsFabric) parseOFportAndIfcName(ofPortAndNameJoined string, ofPortToIfcName map[uint16]string) {
	klog.V(5).Infof("Parsing OpenFlow port number and interface name pair %s from bridge %s",
		ofPortAndNameJoined,
		f.bridge)

	ofPortAndName := strings.Fields(ofPortAndNameJoined)

	// the command that returns interface names for some reason wraps them in
	// double quotes, hence we need to get rid of them
	ofPortAndName[1] = strings.Trim(ofPortAndName[1], "\"")

	// TODO this check is not enough. If there's more than one OvS bridge the
	// interfaces of all bridges are returned, and we might add interfaces which
	// are not part of the bridge this fabric refers to. Fix this. Investigate
	// whether there's an OvS cli option to get interface names and ports from a
	// single bridge. If not think about something else (we could define the
	// interface name to be of a different type than just strings, that enforces
	// a certain pattern). Or we could have this fabric set the name rather than
	// the connection agent.
	if ofPortAndName[1] != f.vtep && ofPortAndName[1] != f.bridge {
		ofPortToIfcName[parseOFport(ofPortAndName[0])] = ofPortAndName[1]
	}
}

func (f *ovsFabric) getFlows() ([]string, error) {
	getFlows := f.newGetFlowsCmd()

	out, err := getFlows.CombinedOutput()
	outStr := strings.TrimRight(string(out), "\n")
	if err != nil {
		return nil, fmt.Errorf("failed to list flows for bridge %s: %s: %s",
			f.bridge,
			err.Error(),
			outStr)
	}

	return parseGetFlowsOutput(outStr), nil
}

// the trailing "Func" stands for functional: this function returns a new slice
// backed by a new array wrt the input slice. A useful flow is a flow which is
// not a tunneling flow, that is, ARP and normal datalink traffic flows
func onlyUsefulLocalFlowsFunc(flows []string) (localFlows []string) {
	localFlows = make([]string, 0, 0)
	for _, aFlow := range flows {
		if isLocal(aFlow) && !isTunneling(aFlow) {
			localFlows = append(localFlows, aFlow)
		}
	}
	return
}

// the trailing "Func" stands for functional: this function returns a new slice
// backed by a new array wrt the input slice
func onlyRemoteFlowsFunc(flows []string) (remoteFlows []string) {
	remoteFlows = make([]string, 0)
	for _, aFlow := range flows {
		if isRemote(aFlow) {
			remoteFlows = append(remoteFlows, aFlow)
		}
	}
	return
}

func isLocal(flow string) bool {
	return strings.Contains(flow, "in_port") ||
		strings.Contains(flow, "actions=output:")
}

func isRemote(flow string) bool {
	return strings.Contains(flow, remoteFlowsFingerprint)
}

func isARP(flow string) bool {
	return strings.Contains(flow, arpFlowsFingerprint)
}

func isTunneling(flow string) bool {
	return strings.Contains(flow, "in_port")
}

func parseGetFlowsOutput(output string) []string {
	flowsLines := strings.Split(output, "\n")
	flows := make([]string, 0, len(flowsLines))
	for _, aFlow := range flowsLines {
		flows = append(flows, string(aFlow))
	}
	return flows
}

// useful means the flow is not a tunneling flow
func (f *ovsFabric) usefulLocalFlowOFport(flow string) uint16 {
	return parseOFport(f.arpOrDlTrafficFlowOFport(flow))
}

func (f *ovsFabric) arpOrDlTrafficFlowOFport(flow string) string {
	return f.flowParsingKit.decNbr.FindString(f.flowParsingKit.output.FindString(flow))
}

func (f *ovsFabric) parseLocalFlowPair(flowsPair []string) *networkfabric.LocalNetIfc {
	ifc := &networkfabric.LocalNetIfc{}

	// both flows in a pair store the vni, we can take it from the first
	// flow without checking its kind
	ifc.VNI = f.extractVNI(flowsPair[0])

	for _, aFlow := range flowsPair {
		if isARP(aFlow) {
			ifc.GuestIP = f.extractGuestIP(aFlow)
		} else {
			ifc.GuestMAC = f.extractGuestMAC(aFlow)
		}
	}

	return ifc
}

func (f *ovsFabric) parseRemoteFlowPair(flowsPair []string) networkfabric.RemoteNetIfc {
	ifc := networkfabric.RemoteNetIfc{}

	// VNI and host IP of a remote interface are stored in both flows created
	// for the interface, thus we can take them from the first flow of the pair
	// without knowing which one it is
	ifc.VNI = f.extractVNI(flowsPair[0])
	ifc.HostIP = f.extractHostIP(flowsPair[0])

	for _, aFlow := range flowsPair {
		if isARP(aFlow) {
			// only the ARP flow stores the Guest IP of the remote interface
			ifc.GuestIP = f.extractGuestIP(aFlow)
		} else {
			// only the Datalink traffifc flow stores the MAC of the interface
			ifc.GuestMAC = f.extractGuestMAC(aFlow)
		}
	}

	return ifc
}

func (f *ovsFabric) extractVNI(flow string) uint32 {
	// flows this method is invoked on (all but tunneling ones) store the vni as
	// a key value pair where the key is "tun_id" and the value is a hex number
	// with a leading "0x". To retrieve it we first get the key value pair with
	// key "tun_id", then we extract the value
	vniHex := f.flowParsingKit.hexNbr.FindString((f.flowParsingKit.tunID.FindString(flow)))
	vni, _ := strconv.ParseUint(strings.TrimLeft(vniHex, hexPrefixChars), 16, 32)
	return uint32(vni)
}

func (f *ovsFabric) extractHostIP(flow string) net.IP {
	// remote flows store the host IP by loading the hex representation of the
	// IP with the load instruction. To retrieve it we first get the load action
	// of the flow and then extract the value from it
	ipHex := f.flowParsingKit.hexNbr.FindString(f.flowParsingKit.load.FindString(flow))
	return hexStrToIPv4(ipHex)
}

func (f *ovsFabric) extractGuestIP(flow string) net.IP {
	// flows store the guest IP as a key value pair where the key is "arp_tpa".
	// get the key value pair, and then extract the IP from it
	guestIP := f.flowParsingKit.ipv4.FindString(f.flowParsingKit.arpTPA.FindString(flow))
	return net.ParseIP(guestIP)
}

func (f *ovsFabric) extractGuestMAC(flow string) net.HardwareAddr {
	// all the flows which store the guest MAC address of an interface store
	// only that MAC address, hence we can directly look for a string matching a
	// MAC address in the flow
	mac, _ := net.ParseMAC(f.flowParsingKit.mac.FindString(flow))
	return mac
}

func (f *ovsFabric) extractCookie(flow string) string {
	return f.flowParsingKit.hexNbr.FindString(f.flowParsingKit.cookie.FindString(flow))
}

func hexStrToIPv4(hexStr string) net.IP {
	i64, _ := strconv.ParseUint(strings.TrimLeft(hexStr, hexPrefixChars), 16, 32)
	i := uint32(i64)
	return net.IPv4(uint8(i>>24), uint8(i>>16), uint8(i>>8), uint8(i))
}

func (f *ovsFabric) newCreateBridgeCmd() *exec.Cmd {
	// enable most OpenFlow protocols because each one has some commands useful
	// for manual inspection of the bridge and its flows. We need at least v1.5
	// because it's the first one to support bundling flows in a single transaction
	// TODO do something better than just hardcoding all the protocols
	return exec.Command("ovs-vsctl",
		"--may-exist",
		"add-br",
		f.bridge,
		"--",
		"set",
		"bridge",
		f.bridge,
		"protocols=OpenFlow10,OpenFlow11,OpenFlow12,OpenFlow13,OpenFlow14,OpenFlow15")
}

func (f *ovsFabric) newAddVTEPCmd() *exec.Cmd {
	return exec.Command("ovs-vsctl",
		"--may-exist",
		"add-port",
		f.bridge,
		f.vtep,
		"--",
		"set",
		"interface",
		f.vtep,
		"type=vxlan",
		"option:key=flow",
		"option:remote_ip=flow")
}

func (f *ovsFabric) newGetIfcOFportCmd(ifc string) *exec.Cmd {
	return exec.Command("ovs-vsctl",
		"get",
		"interface",
		ifc,
		"ofport",
	)
}

func (f *ovsFabric) newCreateIfcCmd(ifc string, mac net.HardwareAddr) *exec.Cmd {
	return exec.Command("ovs-vsctl",
		"add-port",
		f.bridge,
		ifc,
		"--",
		"set",
		"interface",
		ifc,
		"type=internal",
		fmt.Sprintf("mac=%s", strings.Replace(mac.String(), ":", "\\:", -1)))
}

func (f *ovsFabric) newDeleteBridgePortCmd(ifc string) *exec.Cmd {
	return exec.Command("ovs-vsctl",
		"del-port",
		f.bridge,
		ifc)
}

func (f *ovsFabric) newAddFlowsCmd(flows ...string) *exec.Cmd {
	// the --bundle flag makes the addition of the flows transactional, but it
	// works only if the flows are in a file
	cmd := exec.Command("ovs-ofctl", "--bundle", "add-flows", f.bridge, "-")
	cmd.Stdin = strings.NewReader(strings.Join(flows, "\n") + "\n")
	return cmd
}

func (f *ovsFabric) newDelFlowsCmd(flows ...string) *exec.Cmd {
	// the --bundle flag makes the deletion of the flows transactional, but it
	// works only if the flows are in a file
	cmd := exec.Command("ovs-ofctl", "--bundle", "del-flows", f.bridge, "-")
	cmd.Stdin = strings.NewReader(strings.Join(flows, "\n") + "\n")
	return cmd
}

func (f *ovsFabric) newGetFlowsCmd() *exec.Cmd {
	// TODO maybe we can do better: some options might filter out the flows we
	// do not need
	return exec.Command("ovs-ofctl", "dump-flows", f.bridge)
}

func (f *ovsFabric) newListOFportsAndIfcNamesCmd() *exec.Cmd {
	return exec.Command("ovs-vsctl",
		"-f",
		"table",
		"--no-heading",
		"--",
		"--columns=ofport,name",
		"list",
		"Interface")
}

func (f *ovsFabric) lockVNIIPPair(vni uint32, ip net.IP) (err error) {
	vniIP := vniAndIP{
		vni: vni,
		ip:  convert.IPv4ToUint32(ip),
	}
	f.lockedVNIIPPairsMutex.Lock()
	defer func() {
		f.lockedVNIIPPairsMutex.Unlock()
		if err == nil {
			klog.V(5).Infof("Locked IP %s in VNI %#x", ip, vni)
		}
	}()
	if _, pairAlreadyLocked := f.lockedVNIIPPairs[vniIP]; pairAlreadyLocked {
		err = fmt.Errorf("there's already an interface in VNI %#x with IP %d", vni, ip)
		return
	}
	f.lockedVNIIPPairs[vniIP] = struct{}{}
	return
}

func (f *ovsFabric) unlockVNIIPPair(vni uint32, ip net.IP) {
	vniIP := vniAndIP{
		vni: vni,
		ip:  convert.IPv4ToUint32(ip),
	}
	f.lockedVNIIPPairsMutex.Lock()
	defer func() {
		f.lockedVNIIPPairsMutex.Unlock()
		klog.V(5).Infof("Unlocked IP %s in VNI %#x", ip, vni)
	}()
	delete(f.lockedVNIIPPairs, vniIP)
}

type addFlowsErr struct {
	flows, bridge, msg string
}

func (e *addFlowsErr) Error() string {
	return fmt.Sprintf("transaction to add OpenFlow flows %s to bridge %s failed: %s",
		e.flows,
		e.bridge,
		e.msg)
}

func newAddFlowsErr(flows, bridge string, msgs ...string) *addFlowsErr {
	return &addFlowsErr{
		flows:  flows,
		bridge: bridge,
		msg:    strings.Join(msgs, " "),
	}
}

type delFlowsErr struct {
	flows, bridge, msg string
}

func (e *delFlowsErr) Error() string {
	return fmt.Sprintf("transaction to delete OpenFlow flows %s to bridge %s failed: %s",
		e.flows,
		e.bridge,
		e.msg)
}

func newDelFlowsErr(flows, bridge string, msgs ...string) *delFlowsErr {
	return &delFlowsErr{
		flows:  flows,
		bridge: bridge,
		msg:    strings.Join(msgs, " "),
	}
}
