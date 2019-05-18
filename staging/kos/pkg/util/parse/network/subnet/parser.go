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

// Parser is the interface for types that know how to make a Summary out of
// subnets of one or more KOS API versions.
type Parser interface {
	// KnowsVersion returns true if the parser can parse subnet, false
	// otherwise.
	KnowsVersion(subnet interface{}) bool

	// NewSummary returns the Summary of subnet if .KnowsVersion(subnet) returns
	// true, an error otherwise or if the subnet is malformed.
	NewSummary(subnet interface{}) (*Summary, *Error)
}

// parsers is the list of registered parsers.
var parsers []Parser

func RegisterParser(p Parser) {
	parsers = append(parsers, p)
}

type ErrType string

// Types of errors that can occur when parsing a subnet into a Summary.
const (
	UnknownType   ErrType = "UnkownType"
	VNIOutOfRange ErrType = "VNIOutOfRange"
	MalformedCIDR ErrType = "MalformedCIDR"
)

type Error struct {
	Message   string
	ErrorType ErrType
}

var _ error = &Error{}

func (e *Error) Error() string {
	return e.Message
}
