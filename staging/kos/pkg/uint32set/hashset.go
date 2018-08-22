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
	"strings"
)

const Last = uint32(uint64(1)<<32 - 1)

// HashUInt32Set is a UInt32SetChecker with high- and low-water bounds
// tracking, mostly fast operations, a really simple implementation,
// and not great memory efficiency.  The operations take O(1) runtime,
// except for Export and RExport.
type HashUInt32Set struct {
	low, high uint32
	members   map[uint32]struct{}
}

var _ UInt32SetChecker = &HashUInt32Set{}

func NewHashUInt32Set() *HashUInt32Set {
	return &HashUInt32Set{Last, 0, make(map[uint32]struct{})}
}

func (huis *HashUInt32Set) IsEmpty() bool {
	return len(huis.members) == 0
}

func (huis *HashUInt32Set) CouldAddInRange(min, max, x uint32, could bool) bool {
	var c bool
	if x < min || x > max {
		c = false
	} else {
		c = !huis.Has(x)
		if c {
			huis.Add(x)
		}
	}
	return c == could
}

func (huis *HashUInt32Set) Has(x uint32) bool {
	_, ok := huis.members[x]
	return ok
}

func (huis *HashUInt32Set) Add(x uint32) bool {
	_, ok := huis.members[x]
	if ok {
		return false
	}
	huis.members[x] = struct{}{}
	if x < huis.low {
		huis.low = x
	}
	if x > huis.high {
		huis.high = x
	}
	return true
}

func (huis *HashUInt32Set) Remove(x uint32) bool {
	_, ok := huis.members[x]
	if !ok {
		return false
	}
	delete(huis.members, x)
	if len(huis.members) == 0 {
		huis.low, huis.high = Last, 0
	}
	return true
}

func (huis *HashUInt32Set) Export() []uint32 {
	ans := make([]uint32, 0)
	x := huis.low
	for {
		_, ok := huis.members[x]
		if ok {
			ans = append(ans, x)
		}
		if x >= huis.high {
			break
		}
		x++
	}
	return ans
}

// RExport returns the set's membership as a series of runs,
// each run characterized by first and last.
func (huis *HashUInt32Set) RExport() []uint32 {
	ans := make([]uint32, 0)
	isin := false
	x := huis.low
	for {
		_, ok := huis.members[x]
		if ok && !isin {
			ans = append(ans, x)
			isin = true
		} else if isin && !ok {
			ans = append(ans, x-1)
			isin = false
		}
		if x >= huis.high {
			break
		}
		x++
	}
	if isin {
		ans = append(ans, x)
	}
	return ans
}

func (huis *HashUInt32Set) String() string {
	var builder strings.Builder
	builder.WriteString("{")
	x := huis.low
	gotSome := false
	for {
		_, ok := huis.members[x]
		if ok {
			if gotSome {
				builder.WriteString(", ")
			}
			fmt.Fprintf(&builder, "%08x", x)
			gotSome = true
		}
		if x >= huis.high {
			break
		}
		x++
	}
	builder.WriteString("}")
	return builder.String()
}
