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

package subnet

import (
	"fmt"

	"k8s.io/apimachinery/pkg/types"
)

const (
	MinVNI uint32 = 1
	MaxVNI uint32 = 2097151
)

// Summary is a simplified representation of a KOS API Subnet object. It stores
// the information KOS control plane components need to know to carry out their
// tasks.
type Summary struct {
	NamespacedName types.NamespacedName
	VNI            uint32

	// BaseU is the lowest IPv4 address of the subnet as a uint32.
	BaseU uint32

	// LastU is the highest IPv4 address of the subnet as a uint32.
	LastU uint32
}

func NewSummary(subnet interface{}) (*Summary, *Error) {
	for _, p := range parsers {
		if p.KnowsVersion(subnet) {
			return p.NewSummary(subnet)
		}
	}
	return nil, &Error{
		Message:   fmt.Sprintf("type %T of object %#+v is unkown", subnet, subnet),
		ErrorType: UnknownType,
	}
}

// Contains returns true if s2's CIDR is a subset of s1's (regardless of s1 and
// s2 VNIs), false otherwise.
func (s1 *Summary) Contains(s2 *Summary) bool {
	return s1.BaseU <= s2.BaseU && s1.LastU >= s2.LastU
}

func (s1 *Summary) Equal(s2 *Summary) bool {
	return s1.NamespacedName == s2.NamespacedName && s1.VNI == s2.VNI && s1.BaseU == s2.BaseU && s1.LastU == s2.LastU
}

// SameSubnetAs returns true if s1 and s2 have identical namespaced names.
func (s1 *Summary) SameSubnetAs(s2 *Summary) bool {
	return s1.NamespacedName == s2.NamespacedName
}

// CIDRConflict returns true if there is a CIDR conflict between s1 and s2,
// false otherwise. There is a CIDR conflict between s1 and s2 if they have the
// same VNI and their CIDRs overlap.
func (s1 *Summary) CIDRConflict(s2 *Summary) bool {
	return s1.VNI == s2.VNI && s1.BaseU <= s2.LastU && s1.LastU >= s2.BaseU
}

// NSConflict returns true if there is a namespace conflict between s1 and s2,
// false otherwise. There is a namespace conflict between s1 and s2 if they have
// the same VNI but different Kubernetes namespaces.
func (s1 *Summary) NSConflict(s2 *Summary) bool {
	return s1.VNI == s2.VNI && s1.NamespacedName.Namespace != s2.NamespacedName.Namespace
}

// Conflict returns true if s1 and s2 are in any kind of conflict, false
// otherwise. See documentation of NSConflict and CIDRConflict for the possible
// kinds of conflicts.
func (s1 *Summary) Conflict(s2 *Summary) bool {
	return s1.CIDRConflict(s2) || s1.NSConflict(s2)
}
