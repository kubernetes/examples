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

package uint32set_test

import (
	"fmt"
	"math/rand"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/examples/staging/kos/pkg/uint32set"
)

var eis = make([]uint32, 0)

var _ = Describe("UInt32Set", func() {
	var checkLevel int = 2
	Context("Empty set", func() {
		var uis uint32set.UInt32SetChooser
		var hus uint32set.UInt32SetChecker
		BeforeEach(func() {
			uis = uint32set.NewSortedUInt32Set(checkLevel)
			hus = uint32set.NewHashUInt32Set()
		})
		It("is empty", func() {
			Expect(uis.IsEmpty()).To(BeTrue())
			Expect(uis.IsEmpty()).To(BeTrue())
			Expect(uis.Export()).To(Equal(eis))
			Expect(hus.Export()).To(Equal(eis))
			Expect(uis.RExport()).To(Equal(eis))
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			Expect(uis.String()).To(Equal("{}"))
			Expect(hus.String()).To(Equal("{}"))
		})
		It("AddOneInRange gets nothing when limits are reversed", func() {
			Expect(uis.RExport()).To(Equal(eis))
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			_, ok := uis.AddOneInRange(20, 10)
			Expect(ok).To(Equal(false))
		})
		for _, minMax := range []uint32{0, 1, 4294967294, 4294967295} {
			It(fmt.Sprintf("does not have %08x as a member", minMax), func() {
				Expect(uis.Has(minMax)).To(BeFalse())
				Expect(hus.Has(minMax)).To(BeFalse())
				changed := uis.Remove(minMax)
				Expect(changed).To(Equal(false))
				Expect(uis.RExport()).To(Equal(eis))
				changed = hus.Remove(minMax)
				Expect(changed).To(Equal(false))
				Expect(hus.RExport()).To(Equal(eis))
				Expect(uis.String()).To(Equal("{}"))
				Expect(hus.String()).To(Equal("{}"))
				Expect(uis.IsEmpty()).To(Equal(true))
				Expect(hus.IsEmpty()).To(Equal(true))
			})
			It(fmt.Sprintf("AddOneInRange(%x,%x) succeeds once then fails", minMax, minMax), func() {
				i, c := uis.AddOneInRange(minMax, minMax)
				Expect(i).To(Equal(uint32(minMax)))
				Expect(c).To(BeTrue())
				Expect(hus.CouldAddInRange(minMax, minMax, i, c)).To(BeTrue())
				Expect(uis.RExport()).To(Equal(mkslice(minMax, minMax)))
				Expect(uis.RExport()).To(Equal(hus.RExport()))
				Expect(uis.Has(minMax)).To(BeTrue())
				Expect(hus.Has(minMax)).To(BeTrue())
				Expect(uis.Has(minMax ^ 1)).To(BeFalse())
				Expect(hus.Has(minMax ^ 1)).To(BeFalse())
				Expect(uis.IsEmpty()).To(BeFalse())
				Expect(hus.IsEmpty()).To(BeFalse())
				i, c = uis.AddOneInRange(minMax, minMax)
				Expect(c).To(BeFalse())
				Expect(hus.CouldAddInRange(minMax, minMax, i, c)).To(BeTrue())
				Expect(uis.Export()).To(Equal(mkslice(minMax)))
				Expect(uis.RExport()).To(Equal(mkslice(minMax, minMax)))
				Expect(uis.Export()).To(Equal(hus.Export()))
				Expect(uis.RExport()).To(Equal(hus.RExport()))
				Expect(uis.String()).To(Equal(fmt.Sprintf("{%08x--%08x}", minMax, minMax)))
			})
		}
		It("AddOneInRange(0,1) works twice", func() {
			i, c := uis.AddOneInRange(0, 1)
			Expect(c).To(BeTrue())
			Expect(hus.CouldAddInRange(0, 1, i, c)).To(BeTrue())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			Expect(uis.Has(i)).To(BeTrue())
			Expect(hus.Has(i)).To(BeTrue())
			Expect(uis.Has(1 - i)).To(BeFalse())
			Expect(hus.Has(1 - i)).To(BeFalse())
			j, d := uis.AddOneInRange(0, 1)
			Expect(d).To(BeTrue())
			Expect(i+j == 1).To(BeTrue())
			Expect(hus.CouldAddInRange(0, 1, j, d)).To(BeTrue())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			k, e := uis.AddOneInRange(0, 1)
			Expect(e).To(BeFalse())
			Expect(hus.CouldAddInRange(0, 1, k, e)).To(BeTrue())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
		})
		It("preload test", func() {
			c := uis.Add(3)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(mkslice(3, 3)))
			c = hus.Add(3)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			c = uis.Add(2)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(mkslice(2, 3)))
			c = hus.Add(2)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			c = uis.Add(4)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(mkslice(2, 4)))
			c = hus.Add(4)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			i, c := uis.AddOneInRange(0, 4)
			Expect(c).To(BeTrue())
			Expect(hus.CouldAddInRange(0, 4, i, c)).To(BeTrue())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			j, d := uis.AddOneInRange(0, 4)
			Expect(d).To(BeTrue())
			Expect(hus.CouldAddInRange(0, 4, j, d)).To(BeTrue())
			Expect(i < 5 && j < 5 && i+j == 1).To(BeTrue())
			Expect(uis.RExport()).To(Equal(mkslice(0, 4)))
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			i, c = uis.AddOneInRange(0, 4)
			Expect(c).To(BeFalse())
			Expect(hus.CouldAddInRange(0, 4, i, c)).To(BeTrue())
			Expect(uis.RExport()).To(Equal(mkslice(0, 4)))
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			Expect(uis.String()).To(Equal("{00000000--00000004}"))
			c = uis.Remove(4)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(mkslice(0, 3)))
			c = hus.Remove(4)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			c = uis.Remove(4)
			Expect(c).To(BeFalse())
			Expect(uis.RExport()).To(Equal(mkslice(0, 3)))
			c = hus.Remove(4)
			Expect(c).To(BeFalse())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			c = uis.Remove(0)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(mkslice(1, 3)))
			c = hus.Remove(0)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			c = uis.Remove(2)
			Expect(c).To(BeTrue())
			Expect(uis.Export()).To(Equal(mkslice(1, 3)))
			Expect(uis.RExport()).To(Equal(mkslice(1, 1, 3, 3)))
			Expect(uis.String()).To(Equal("{00000001--00000001, 00000003--00000003}"))
			c = hus.Remove(2)
			Expect(c).To(BeTrue())
			Expect(uis.Export()).To(Equal(hus.Export()))
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			Expect(hus.String()).To(Equal("{00000001, 00000003}"))
			c = uis.Remove(1)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(mkslice(3, 3)))
			c = hus.Remove(1)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			c = uis.Remove(3)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(eis))
			c = hus.Remove(3)
			Expect(c).To(BeTrue())
			Expect(uis.RExport()).To(Equal(hus.RExport()))
			Expect(uis.IsEmpty()).To(BeTrue())
			Expect(hus.IsEmpty()).To(BeTrue())
		})
		for _, high := range []bool{false, true} {
			min := uint32(0)
			max := uint32(15)
			minTry := uint32(0)
			end := "low"
			if high {
				max = uint32(uint64(1)<<32 - 1)
				min = max - 15
				minTry = max - 16
				end = "high"
			}
			for i := 1; i <= 2500; i++ {
				It(fmt.Sprintf("passes random test %d at the %s end of the range of uint32", i, end), func() {
					for j := 0; j < 512; j++ {
						op := rand.Intn(100)
						if op < 12 {
							x := min + uint32(rand.Intn(16))
							c1 := uis.Add(x)
							c2 := hus.Add(x)
							Expect(c1).To(Equal(c2))
							Expect(uis.Has(x)).To(BeTrue())
							Expect(hus.Has(x)).To(BeTrue())
						} else if op < 26 {
							x, c := uis.AddOneInRange(min+2, min+6)
							Expect(hus.CouldAddInRange(min+2, min+6, x, c)).To(BeTrue())
							if c {
								Expect(uis.Has(x)).To(BeTrue())
							}
						} else if op < 40 {
							x, c := uis.AddOneInRange(min+9, min+13)
							Expect(hus.CouldAddInRange(min+9, min+13, x, c)).To(BeTrue())
							if c {
								Expect(uis.Has(x)).To(BeTrue())
							}
						} else {
							x := minTry + uint32(rand.Intn(17))
							c1 := uis.Remove(x)
							c2 := hus.Remove(x)
							Expect(c1).To(Equal(c2))
							Expect(uis.Has(x)).To(BeFalse())
							Expect(hus.Has(x)).To(BeFalse())
							if x < min || x > max {
								Expect(c1).To(BeFalse())
							}
						}
						Expect(uis.Export()).To(Equal(hus.Export()))
						Expect(uis.RExport()).To(Equal(hus.RExport()))
						Expect(uis.IsEmpty()).To(Equal(hus.IsEmpty()))
					}
				})
			}
		}
	})
})

func mkslice(x ...uint32) []uint32 {
	return x
}
