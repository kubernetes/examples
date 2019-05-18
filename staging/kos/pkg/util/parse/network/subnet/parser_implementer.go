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
	"net"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/examples/staging/kos/pkg/apis/network"
	"k8s.io/examples/staging/kos/pkg/util/convert"
)

func init() {
	RegisterParser(&parser{})
}

// parser implements a Parser that can parse KOS API subnet objects of the
// internal version.
type parser struct{}

var _ Parser = &parser{}

func (p *parser) KnowsVersion(allegedInternalVersionSubnet interface{}) bool {
	_, isInternalVersionSubnet := allegedInternalVersionSubnet.(*network.Subnet)
	return isInternalVersionSubnet
}

func (p *parser) NewSummary(allegedInternalVersionSubnet interface{}) (*Summary, *Error) {
	s, isInternalVersionSubnet := allegedInternalVersionSubnet.(*network.Subnet)
	if !isInternalVersionSubnet {
		return nil, &Error{
			Message:   fmt.Sprintf("parser can only parse type %T, object %#+v has type %T", &network.Subnet{}, s, s),
			ErrorType: UnknownType,
		}
	}

	return newSummary(s)
}

func newSummary(s *network.Subnet) (*Summary, *Error) {
	if s.Spec.VNI < MinVNI || s.Spec.VNI > MaxVNI {
		return nil, &Error{
			Message:   fmt.Sprintf("VNI (%d) must be in [%d,%d]", s.Spec.VNI, MinVNI, MaxVNI),
			ErrorType: VNIOutOfRange,
		}
	}

	_, ipNet, err := net.ParseCIDR(s.Spec.IPv4)
	if err != nil {
		return nil, &Error{
			Message:   fmt.Sprintf("invalid CIDR: %s", err.Error()),
			ErrorType: MalformedCIDR,
		}
	}

	baseU, lastU := convert.IPNetToBoundsU(ipNet)

	return &Summary{
		NamespacedName: types.NamespacedName{
			Namespace: s.Namespace,
			Name:      s.Name,
		},
		VNI:   s.Spec.VNI,
		BaseU: baseU,
		LastU: lastU,
	}, nil
}
