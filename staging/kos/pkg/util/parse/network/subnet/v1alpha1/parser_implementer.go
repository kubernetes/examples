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

package v1alpha1

import (
	"fmt"
	"net"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
	"k8s.io/examples/staging/kos/pkg/util/convert"
	"k8s.io/examples/staging/kos/pkg/util/parse/network/subnet"
)

func init() {
	subnet.RegisterParser(&parser{})
}

// parser implements a Parser that can parse KOS API subnet objects of the
// v1alpha1 version.
type parser struct{}

var _ subnet.Parser = &parser{}

func (p *parser) KnowsVersion(allegedV1alpha1Subnet interface{}) bool {
	_, isV1alpha1Subnet := allegedV1alpha1Subnet.(*v1alpha1.Subnet)
	return isV1alpha1Subnet
}

func (p *parser) NewSummary(allegedV1alpha1Subnet interface{}) (*subnet.Summary, subnet.Errors) {
	s, isV1alpha1Subnet := allegedV1alpha1Subnet.(*v1alpha1.Subnet)
	if !isV1alpha1Subnet {
		err := &subnet.Error{
			Message: fmt.Sprintf("parser can only parse type %T, object %#+v has type %T", &v1alpha1.Subnet{}, s, s),
			Reason:  subnet.UnknownType,
		}
		return nil, []*subnet.Error{err}
	}

	return newSummary(s)
}

func newSummary(s *v1alpha1.Subnet) (*subnet.Summary, subnet.Errors) {
	var errs []*subnet.Error

	// Check VNI is within allowed range.
	if s.Spec.VNI < subnet.MinVNI || s.Spec.VNI > subnet.MaxVNI {
		e := &subnet.Error{
			Message: fmt.Sprintf("vni (%d) must be in [%d,%d]", s.Spec.VNI, subnet.MinVNI, subnet.MaxVNI),
			Reason:  subnet.VNIOutOfRange,
		}
		errs = append(errs, e)
	}

	// Check CIDR is well-formed and extract lowest and highest addresses.
	var baseU, lastU uint32
	_, ipNet, err := net.ParseCIDR(s.Spec.IPv4)
	if err == nil {
		baseU, lastU = convert.IPNetToBoundsU(ipNet)
	} else {
		e := &subnet.Error{
			Message: err.Error(),
			Reason:  subnet.MalformedCIDR,
		}
		errs = append(errs, e)
	}

	return &subnet.Summary{
		NamespacedName: types.NamespacedName{
			Namespace: s.Namespace,
			Name:      s.Name,
		},
		VNI:   s.Spec.VNI,
		BaseU: baseU,
		LastU: lastU,
	}, errs
}
