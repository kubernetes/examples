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
	"math/rand"
	"strings"

	"k8s.io/klog"

	"github.com/wangjia184/sortedset"
)

// SortedUInt32Set is a UInt32SetChooser with fast operations.
// The representation is based on a sorted list of runs.
// Each operation's runtime is logarithmic in the number of runs.
// NONE of the operations is thread-safe, NOT EVEN String().
type SortedUInt32Set struct {
	// SortedSet holds runs of uints.  For each node in the SortedSet,
	// the key is the first uint32 of the run as hex digits, the score
	// is the first uint32, and the value is the last uint32 as a
	// uint32.  Runs do not overlap.  Between any two consecutive runs
	// there is a gap containing at least one number not in the set.
	*sortedset.SortedSet

	// CheckLevel indicates how much self-checking should be included
	// in each operation.  0 is none.  1 is quick.  2 is exhaustive.
	CheckLevel int
}

var _ UInt32SetChooser = &SortedUInt32Set{}

// NewSortedUInt32Set makes a new SortedUInt32Set with the given checking level.
func NewSortedUInt32Set(checkLevel int) *SortedUInt32Set {
	return &SortedUInt32Set{sortedset.New(), checkLevel}
}

func (uis *SortedUInt32Set) IsEmpty() bool {
	return uis.SortedSet.GetCount() == 0
}

func (uis *SortedUInt32Set) Has(x uint32) bool {
	node, _ := uis.Find(x)
	return node != nil
}

// Add ensures that the given number is in the set.  The returned bool
// is `true` if the number was not already in the set.  The runtime
// cost is O(log N), where N is the number of runs in the set.
func (uis *SortedUInt32Set) Add(x uint32) bool {
	xS := fmt.Sprintf("%08x", x)
	xN := uis.SortedSet.GetByKey(xS)
	if xN != nil {
		return false
	}
	// Oh the pain
	uis.SortedSet.AddOrUpdate(xS, sortedset.SCORE(x), x)
	rankX := uis.SortedSet.FindRank(xS)
	mergeWithPrev := false
	var prevNode *sortedset.SortedSetNode
	if rankX > 1 {
		prevNode = uis.SortedSet.GetByRank(rankX-1, false)
		prevLast := prevNode.Value.(uint32)
		if x <= prevLast {
			uis.SortedSet.Remove(xS)
			return false
		}
		mergeWithPrev = prevLast+1 == x
	}
	nextNode := uis.SortedSet.GetByRank(rankX+1, false)
	mergeWithNext := nextNode != nil && sortedset.SCORE(x+1) == nextNode.Score()
	klog.V(5).Infof("\nAdd.In-midst: ss=%#+v, x=%08x, prevNode=%#+v, nextNode=%#+v", *uis.SortedSet, x, prevNode, nextNode)
	if mergeWithPrev {
		uis.SortedSet.Remove(xS)
		if mergeWithNext {
			nextLast := nextNode.Value
			uis.SortedSet.Remove(nextNode.Key())
			uis.SortedSet.AddOrUpdate(prevNode.Key(), prevNode.Score(), nextLast)
		} else {
			uis.SortedSet.AddOrUpdate(prevNode.Key(), prevNode.Score(), x)
		}
	} else if mergeWithNext {
		uis.SortedSet.Remove(nextNode.Key())
		uis.SortedSet.AddOrUpdate(xS, sortedset.SCORE(x), nextNode.Value)
	} else {
	}
	klog.V(5).Infof("Add.After: ss=%#+v", *uis.SortedSet)
	if uis.CheckLevel > 0 {
		uis.Check(uis.CheckLevel > 1)
	}
	return true
}

// Check runs some data structure integrity checks.  If `hard` then the
// checks are relatively extensive and cost O(N log N) run time,
// otherwise the checks cost O(log N) run time, where N is the number
// of runs in the set.
func (uis *SortedUInt32Set) Check(hard bool) {
	if uis.SortedSet.GetCount() == 0 {
		if uis.SortedSet.GetByRank(1, false) != nil {
			panic(fmt.Sprintf("%#+v has wrong GetCount()", *uis))
		}
	} else {
		firstNode := uis.SortedSet.GetByRank(1, false)
		if firstNode == nil {
			panic(fmt.Sprintf("%#+v has no nodes", *uis))
		}
		if uint32(firstNode.Score()) > firstNode.Value.(uint32) {
			panic(fmt.Sprintf("%#+v has inverted first node %#+v", *uis, *firstNode))
		}
		count := uis.SortedSet.GetCount()
		lastNode := firstNode
		if hard {
			for rank := 2; rank <= count; rank++ {
				node := uis.SortedSet.GetByRank(rank, false)
				if node == nil {
					panic(fmt.Sprintf("%#+v lacks expected node at rank %d", *uis, rank))
				}
				if uint32(node.Score()) <= lastNode.Value.(uint32)+1 {
					panic(fmt.Sprintf("%#+v has bad gap from %d=%#+v to %#+v", *uis, rank-1, lastNode, node))
				}
				if uint32(node.Score()) > node.Value.(uint32) {
					panic(fmt.Sprintf("%#+v has inverted rank=%d node %#+v", *uis, rank, *node))
				}
				lastNode = node
			}
		} else {
			lastNode = uis.SortedSet.GetByRank(count, false)
			if lastNode == nil {
				panic(fmt.Sprintf("%#+v has GetCount()=%d too big", *uis, count))
			}
		}
		if uint32(lastNode.Score()) > lastNode.Value.(uint32) {
			panic(fmt.Sprintf("%#+v has inverted last node %#+v", *uis, *lastNode))
		}
	}
	if uis.SortedSet.GetByRank(uis.SortedSet.GetCount()+1, false) != nil {
		panic(fmt.Sprintf("%#+v has GetCount() too small", *uis))
	}

}

// AddOne picks a number that is in the given range (inclusive) and
// not already in the set and adds it, if there are any such numbers.
// If so, that number and `true` are returned.  Otherwise some number
// and `false` are returned.  The runtime cost is O(log N), where N is
// the number of runs in the set.
func (uis *SortedUInt32Set) AddOneInRange(min, max uint32) (x uint32, ok bool) {
	if max < min {
		return 0, false
	}
	// First, identify and count the gaps.  The gaps are numbered 0
	// through nGaps-1.  Gap 0, which might be empty, starts at min
	// and ends just before the lowest set member in the given range
	// if there is such a member otherwise the end of the given range.
	// If nGaps>1 then gap nGaps-1, which might be empty, ends at max
	// and starts right after the highest set member in the given
	// range.  The other gaps are certainly non-empty, with gap i
	// between the runs at rank minR+i-1 and rank minR+i.
	onMin, minR := uis.Find(min)
	onMax, maxR := uis.Find(max)
	if onMin == onMax && onMin != nil {
		return 0, false
	}
	nGaps := 1 + maxR - minR
	var bias, saib int // number of gaps to avoid at front, back
	if onMin != nil {
		bias = 1
	}
	if onMax != nil {
		saib = 1
		nGaps++
	}
	if nGaps <= bias+saib {
		panic(fmt.Sprintf("AddOneInRange fail ss=%#+v, min=%08x, max=%08x, nGaps=%d, bias=%d, saib=%d", *uis, min, max, nGaps, bias, saib))
	}

	// Next, pick a random gap
	gapNum := bias + rand.Intn(nGaps-(bias+saib))

	// Now get its bounds and pick a random member
	var prevNode, nextNode *sortedset.SortedSetNode
	var first, last uint32
	if gapNum > 0 {
		prevNode = uis.SortedSet.GetByRank(minR+gapNum-1, false)
		if prevNode == nil {
			panic(fmt.Sprintf("%#+v lacks expected run %d+%d-1", *uis, minR, gapNum))
		}
		first = prevNode.Value.(uint32) + 1
	} else {
		first = min
	}
	if gapNum < nGaps-1 {
		nextNode = uis.SortedSet.GetByRank(minR+gapNum, false)
		if nextNode == nil {
			panic(fmt.Sprintf("%#+v lacks expected run %d+%d", *uis, minR, gapNum))
		}
		last = uint32(nextNode.Score()) - 1
	} else {
		last = max
	}
	x = first + uint32(rand.Int63n(1+int64(last-first)))

	// Finally, add the chosen number to the set
	ok = uis.Add(x)
	if !ok {
		panic(fmt.Sprintf("%08x chosen from gap %d was already in the set %#+v", x, gapNum, *uis.SortedSet))
	}
	return x, true
}

// find locates the given number among the runs.
// node, if not nil, is the run that includes x.
// rank is the rank of the run that includes or would include x.
func (uis *SortedUInt32Set) Find(x uint32) (node *sortedset.SortedSetNode, rank int) {
	xS := fmt.Sprintf("%08x", x)
	rank = uis.SortedSet.FindRank(xS)
	if rank > 0 {
		return uis.SortedSet.GetByRank(rank, false), rank
	}
	uis.SortedSet.AddOrUpdate(xS, sortedset.SCORE(x), x)
	rankNext := uis.SortedSet.FindRank(xS)
	uis.SortedSet.Remove(xS)
	if rankNext > 1 {
		anode := uis.SortedSet.GetByRank(rankNext-1, false)
		if x <= anode.Value.(uint32) {
			return anode, rankNext - 1
		}
	}
	return nil, rankNext
}

// Remove ensures the given number is not in the set.
// The return value is `true` iff the number was in the set.
// The runtime cost is O(log N), where N is the number of runs in the set.
func (uis *SortedUInt32Set) Remove(x uint32) bool {
	klog.V(5).Infof("\nRemove(%08x).Before: ss=%#+v", x, *uis.SortedSet)
	ans := uis.innerRemove(x)
	klog.V(5).Infof("Remove(%08x)=%v.After: ss=%#+v", x, ans, *uis.SortedSet)
	if uis.CheckLevel > 0 {
		uis.Check(uis.CheckLevel > 1)
	}
	return ans
}

func (uis *SortedUInt32Set) innerRemove(x uint32) bool {
	xS := fmt.Sprintf("%08x", x)
	xN := uis.SortedSet.GetByKey(xS)
	if xN != nil {
		uis.SortedSet.Remove(xS)
		last := xN.Value.(uint32)
		if x != last {
			y := x + 1
			yS := fmt.Sprintf("%08x", y)
			uis.SortedSet.AddOrUpdate(yS, sortedset.SCORE(y), xN.Value)
		}
	} else {
		// Oh the pain
		uis.SortedSet.AddOrUpdate(xS, sortedset.SCORE(x), x)
		rankX := uis.SortedSet.FindRank(xS)
		uis.SortedSet.Remove(xS)
		if rankX > 1 {
			prevNode := uis.SortedSet.GetByRank(rankX-1, false)
			prevLast := prevNode.Value.(uint32)
			if x > prevLast {
				return false
			}
			uis.SortedSet.AddOrUpdate(prevNode.Key(), prevNode.Score(), x-1)
			if x < prevLast { // split prevNode in two
				y := x + 1
				yS := fmt.Sprintf("%08x", y)
				uis.SortedSet.AddOrUpdate(yS, sortedset.SCORE(y), prevLast)
			}
		} else {
			return false
		}
	}
	return true
}

// Export returns the set's membership as a slice of uint32
func (uis *SortedUInt32Set) Export() []uint32 {
	ans := make([]uint32, 0)
	rank := 1
	for {
		node := uis.SortedSet.GetByRank(rank, false)
		if node == nil {
			return ans
		}
		i := uint32(node.Score())
		l := node.Value.(uint32)
		for x := i; true; {
			ans = append(ans, x)
			if x >= l {
				break
			}
			x++
		}
		rank++
	}
}

// RExport returns the set's membership as a series of runs,
// each run characterized by first and last.
func (uis *SortedUInt32Set) RExport() []uint32 {
	ans := make([]uint32, 0)
	rank := 1
	for {
		node := uis.SortedSet.GetByRank(rank, false)
		if node == nil {
			return ans
		}
		i := uint32(node.Score())
		l := node.Value.(uint32)
		ans = append(ans, i, l)
		rank++
	}
}

func (uis *SortedUInt32Set) String() string {
	runs := uis.RExport()
	var builder strings.Builder
	builder.WriteString("{")
	for i := 0; i < len(runs); i += 2 {
		if i > 0 {
			builder.WriteString(", ")
		}
		fmt.Fprintf(&builder, "%08x--%08x", runs[i], runs[i+1])
	}
	builder.WriteString("}")
	return builder.String()
}
