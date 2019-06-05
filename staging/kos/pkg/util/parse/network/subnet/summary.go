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
	gonet "net"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	internal "k8s.io/examples/staging/kos/pkg/apis/network"
	"k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
	"k8s.io/examples/staging/kos/pkg/util/convert"
)

const (
	minVNI uint32 = 1
	maxVNI uint32 = 2097151
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

type ErrReason string

// Types of errors that can occur when parsing a subnet into a Summary.
const (
	UnknownType   ErrReason = "UnkownType"
	VNIOutOfRange ErrReason = "VNIOutOfRange"
	MalformedCIDR ErrReason = "MalformedCIDR"
)

type Error struct {
	Message string
	Reason  ErrReason
}

var _ error = &Error{}

func (e *Error) Error() string {
	return e.Message
}

type Errors []*Error

var _ error = Errors{}

func (errs Errors) Error() string {
	msg := ""
	for _, e := range errs {
		msg = fmt.Sprintf("%s, %s", msg, e.Error())
	}
	return strings.Trim(msg, ", ")
}

func NewSummary(subnet interface{}) (*Summary, Errors) {
	switch sn := subnet.(type) {
	case *internal.Subnet:
		return makeSummary(sn.Namespace, sn.Name, sn.Spec.VNI, sn.Spec.IPv4)
	case *v1alpha1.Subnet:
		return makeSummary(sn.Namespace, sn.Name, sn.Spec.VNI, sn.Spec.IPv4)
	default:
		return nil, []*Error{&Error{
			Message: fmt.Sprintf("type %T of object %#+v is unknown", subnet, subnet),
			Reason:  UnknownType},
		}
	}
}

func makeSummary(ns, name string, vni uint32, ipv4 string) (summary *Summary, errs Errors) {
	// Check VNI is within allowed range.
	if vni < minVNI || vni > maxVNI {
		e := &Error{
			Message: fmt.Sprintf("vni (%d) must be in [%d,%d]", vni, minVNI, maxVNI),
			Reason:  VNIOutOfRange,
		}
		errs = append(errs, e)
	}

	// Check CIDR is well-formed and extract lowest and highest addresses.
	var baseU, lastU uint32
	_, ipNet, err := gonet.ParseCIDR(ipv4)
	if err == nil {
		baseU, lastU = convert.IPNetToBoundsU(ipNet)
	} else {
		e := &Error{
			Message: err.Error(),
			Reason:  MalformedCIDR,
		}
		errs = append(errs, e)
	}

	summary = &Summary{
		NamespacedName: types.NamespacedName{
			Namespace: ns,
			Name:      name,
		},
		VNI:   vni,
		BaseU: baseU,
		LastU: lastU,
	}
	return
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
