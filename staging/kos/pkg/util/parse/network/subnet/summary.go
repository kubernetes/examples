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

	// Lower and upper bounds for RFC 1918 ranges of IP addresses.
	rfc1918LB1 uint32 = 0xa000000
	rfc1918UB1 uint32 = 0xaffffff
	rfc1918LB2 uint32 = 0xac100000
	rfc1918UB2 uint32 = 0xac1fffff
	rfc1918LB3 uint32 = 0xc0a80000
	rfc1918UB3 uint32 = 0xc0a8ffff
)

// Summary is a simplified representation of a KOS API Subnet object. It stores
// the information KOS control plane components need to know to carry out their
// tasks.
type Summary struct {
	NamespacedName types.NamespacedName
	UID            types.UID
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
		return makeSummary(sn.Namespace, sn.Name, sn.UID, sn.Spec.VNI, sn.Spec.IPv4)
	case *v1alpha1.Subnet:
		return makeSummary(sn.Namespace, sn.Name, sn.UID, sn.Spec.VNI, sn.Spec.IPv4)
	default:
		return nil, []*Error{&Error{
			Message: fmt.Sprintf("type %T of object %#+v is unknown", subnet, subnet),
			Reason:  UnknownType},
		}
	}
}

func makeSummary(ns, name string, uid types.UID, vni uint32, ipv4 string) (summary *Summary, errs Errors) {
	// Check VNI is within allowed range.
	if vni < minVNI || vni > maxVNI {
		errs = append(errs, &Error{
			Message: fmt.Sprintf("must be in [%d,%d]", minVNI, maxVNI),
			Reason:  VNIOutOfRange,
		})
	}

	// Check CIDR is well-formed and compliant with https://tools.ietf.org/html/rfc1918
	// because the algorithm that generates MAC addresses for NetworkAttachments
	// is collision-free only assuming such compliance.
	var baseU, lastU uint32
	_, ipNet, err := gonet.ParseCIDR(ipv4)
	if err == nil {
		baseU, lastU = convert.IPNetToBoundsU(ipNet)
		if baseU < rfc1918LB1 || lastU > rfc1918UB1 && baseU < rfc1918LB2 || lastU > rfc1918UB2 && baseU < rfc1918LB3 || lastU > rfc1918UB3 {
			errs = append(errs, &Error{
				Message: "must be compliant with RFC 1918 and is not",
				Reason:  MalformedCIDR,
			})
		}
	} else {
		errs = append(errs, &Error{
			Message: err.Error(),
			Reason:  MalformedCIDR,
		})
	}

	if len(errs) == 0 {
		summary = &Summary{
			NamespacedName: types.NamespacedName{
				Namespace: ns,
				Name:      name,
			},
			UID:   uid,
			VNI:   vni,
			BaseU: baseU,
			LastU: lastU,
		}
	}
	return
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
