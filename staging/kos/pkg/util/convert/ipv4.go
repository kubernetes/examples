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
	"encoding/binary"
	"net"
)

func IPv4ToUint32(ip net.IP) uint32 {
	if len(ip) > 4 {
		return binary.BigEndian.Uint32(ip[len(ip)-4:])
	}
	return binary.BigEndian.Uint32(ip)
}

func Uint32ToIPv4(i uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, i)
	return ip
}

func IPNetToBoundsU(ipNet *net.IPNet) (min, max uint32) {
	min = IPv4ToUint32(ipNet.IP)
	ones, bits := ipNet.Mask.Size()
	delta := uint32(uint64(1)<<uint(bits-ones) - 1)
	max = min + delta
	return
}
