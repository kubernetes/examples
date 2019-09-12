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
	gonet "net"
	"strconv"
	"strings"

	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfields "k8s.io/apimachinery/pkg/fields"
	k8scache "k8s.io/client-go/tools/cache"

	netv1a1 "k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
	kosinternalifcs "k8s.io/examples/staging/kos/pkg/client/informers/externalversions/internalinterfaces"
)

type sliceOfString []string

func (x sliceOfString) Equal(y sliceOfString) bool {
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

// attVNIAndIP is an Index function that computes a string made up by vni and IP
// of a NetworkAttachment. Used to sync pre-existing local network interfaces
// with local network attachments at start up.
func attVNIAndIP(obj interface{}) ([]string, error) {
	att := obj.(*netv1a1.NetworkAttachment)
	return []string{strconv.FormatUint(uint64(att.Status.AddressVNI), 16) + "/" + att.Status.IPv4}, nil
}

var _ k8scache.IndexFunc = attVNIAndIP

// attHostIPAndIP is an Index function that computes a string made up by host IP
// and IP of a NetworkAttachment. Used to sync pre-existing remote network
// interfaces with remote network attachments at start up.
func attHostIPAndIP(obj interface{}) ([]string, error) {
	att := obj.(*netv1a1.NetworkAttachment)
	return []string{att.Status.HostIP + "/" + att.Status.IPv4}, nil
}

var _ k8scache.IndexFunc = attHostIPAndIP

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
		for {
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
		}
	}()
	return aggregateStopCh
}

func formatErrVal(err bool) string {
	if err {
		return "err"
	}
	return "ok"
}
