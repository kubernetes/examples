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

package uint32set

import (
	"fmt"
)

/* UInt32Set is a variable set of uint32.
   None of the operations is thread safe. */

type UInt32Set interface {
	fmt.Stringer

	// IsEmpty indicates whether the set is empty.
	IsEmpty() bool

	// Has indicates whether the given number is in the set.
	Has(uint32) bool

	// Add returns true and adds the given value to the set if that
	// value was not already in the set.  Otherwise returns false.
	Add(uint32) bool

	// Remove returns true and removes the given value from the set if
	// that value was in the set.  Otherwise returns false.
	Remove(uint32) bool

	// Export returns the members of the set as a slice.
	Export() []uint32

	// RExport returns the members of the set as a series of runs,
	// each run characterized by first and last value.
	RExport() []uint32
}

/* UInt32SetChooser extends UInt32Set with operations to pick new members
   for the set. */

type UInt32SetChooser interface {
	UInt32Set

	// AddOneInRange picks a number that is in the given range
	// (inclusive) and not already in the set and adds it, if there
	// are any such numbers.  If so, the chosen number and `true` are
	// returned.  Otherwise some number and `false` are returned.
	AddOneInRange(min, max uint32) (x uint32, ok bool)
}

/* UInt32SetChecker extends UInt32Set with operations that check the
   validity of corresponding extensions in UInt32SetChooser. */

type UInt32SetChecker interface {
	UInt32Set

	// CouldAddInRange indicates whether a call to
	// `AddOneInRange(min,max)` could return `(x,ok)`, and does the
	// add if `ok`.
	CouldAddInRange(min, max uint32, x uint32, ok bool) bool
}
