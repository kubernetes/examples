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

package util

import (
	"encoding/binary"
	"net"
)

func MapStringStringGet(m map[string]string, i string) (mi string) {
	if m != nil {
		mi = m[i]
	}
	return
}

// makeUint32From4Bytes makes a uint32 from a byte array
func MakeUint32FromIPv4(ip []byte) uint32 {
	var iv uint32
	if len(ip) > 4 {
		iv = binary.BigEndian.Uint32(ip[len(ip)-4:])
	} else {
		iv = binary.BigEndian.Uint32(ip)
	}
	return iv
}

func MakeIPv4FromUint32(n uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, n)
	return ip
}

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
