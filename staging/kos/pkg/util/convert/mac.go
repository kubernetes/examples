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

package convert

import (
	"net"
)

// MACAddressToUint64 encodes a MAC address in a uint64.
// NOTE: It assumes that `mac` fits in 64 bits.
func MACAddressToUint64(mac net.HardwareAddr) (v uint64) {
	var shift uint64 = 256
	for _, b := range mac {
		v = v*shift + uint64(b)
	}
	return
}
