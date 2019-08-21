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
	"bytes"
	"fmt"
	gonet "net"
	"strconv"
	"strings"

	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfields "k8s.io/apimachinery/pkg/fields"

	kosinternalifcs "k8s.io/examples/staging/kos/pkg/client/informers/externalversions/internalinterfaces"
	netfabric "k8s.io/examples/staging/kos/pkg/networkfabric"
)

type SliceOfString []string

func (x SliceOfString) Equal(y SliceOfString) bool {
	if len(x) != len(y) {
		return false
	}
	for i, xi := range x {
		if xi != y[i] {
			return false
		}
	}
	return true
}

type fieldsSelector struct {
	k8sfields.Selector
}

func (fs fieldsSelector) toTweakListOptionsFunc() kosinternalifcs.TweakListOptionsFunc {
	return func(options *k8smetav1.ListOptions) {
		optionsFieldSelector := options.FieldSelector

		allSelectors := make([]string, 0, 2)
		if strings.Trim(optionsFieldSelector, " ") != "" {
			allSelectors = append(allSelectors, optionsFieldSelector)
		}
		allSelectors = append(allSelectors, fs.String())

		options.FieldSelector = strings.Join(allSelectors, ",")
	}
}

func ifcNeedsUpdate(ifcHostIP, newHostIP gonet.IP, ifcMAC, newMAC gonet.HardwareAddr) bool {
	return !ifcHostIP.Equal(newHostIP) || !bytes.Equal(ifcMAC, newMAC)
}

func attVNIAndIP(vni uint32, ipv4 string) string {
	return strconv.FormatUint(uint64(vni), 16) + "/" + ipv4
}

func localIfcVNIAndIP(ifc *netfabric.LocalNetIfc) string {
	return ifcVNIAndIP(ifc.VNI, ifc.GuestIP)
}

func remoteIfcVNIAndIP(ifc *netfabric.RemoteNetIfc) string {
	return ifcVNIAndIP(ifc.VNI, ifc.GuestIP)
}

func ifcVNIAndIP(vni uint32, ipv4 gonet.IP) string {
	return strconv.FormatUint(uint64(vni), 16) + "/" + ipv4.String()
}

func generateMACAddr(vni uint32, guestIPv4 gonet.IP) gonet.HardwareAddr {
	guestIPBytes := guestIPv4.To4()
	mac := make([]byte, 6, 6)
	mac[5] = byte(vni)
	mac[4] = byte(vni >> 8)
	mac[3] = guestIPBytes[3]
	mac[2] = guestIPBytes[2]
	mac[1] = guestIPBytes[1]
	mac[0] = (byte(vni>>13) & 0xF8) | ((guestIPBytes[0] & 0x02) << 1) | 2
	return mac
}

func generateIfcName(macAddr gonet.HardwareAddr) string {
	return "kos" + strings.Replace(macAddr.String(), ":", "", -1)
}

// mergeStopChannels returns a channel which is closed when either ch1 or ch2 is
// closed.
func mergeStopChannels(ch1, ch2 <-chan struct{}) chan struct{} {
	aggregateStopCh := make(chan struct{})
	go func() {
		select {
		case _, ch1Open := <-ch1:
			if !ch1Open {
				close(aggregateStopCh)
				return
			}
		case _, ch2Open := <-ch2:
			if !ch2Open {
				close(aggregateStopCh)
				return
			}
		}
	}()
	return aggregateStopCh
}

func aggregateErrors(sep string, errs ...error) error {
	aggregateErrsSlice := make([]string, 0, len(errs))
	for i, err := range errs {
		if err != nil && strings.Trim(err.Error(), " ") != "" {
			aggregateErrsSlice = append(aggregateErrsSlice, fmt.Sprintf("error nbr. %d ", i)+err.Error())
		}
	}
	if len(aggregateErrsSlice) > 0 {
		return fmt.Errorf("%s", strings.Join(aggregateErrsSlice, sep))
	}
	return nil
}

func FormatErrVal(err bool) string {
	if err {
		return "err"
	}
	return "ok"
}
