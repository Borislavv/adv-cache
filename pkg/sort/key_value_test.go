package sort

import (
	"bytes"
	"math/rand"
	gosort "sort"
	"testing"
)

func makeTestSlice(n int) [][2][]byte {
	s := make([][2][]byte, n)
	for i := 0; i < n; i++ {
		b := make([]byte, 8)
		rand.Read(b)
		s[i][0] = b
		s[i][1] = []byte("value") // dummy payload
	}
	return s
}

func clone(in [][2][]byte) [][2][]byte {
	out := make([][2][]byte, len(in))
	copy(out, in)
	for i := range in {
		// ensure inner slices are copied
		out[i][0] = append([]byte(nil), in[i][0]...)
		out[i][1] = append([]byte(nil), in[i][1]...)
	}
	return out
}

func BenchmarkSortKV_Custom(b *testing.B) {
	data := makeTestSlice(24)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := clone(data)
		KVSlice(buf)
	}
}

func BenchmarkSortKV_StdSlice(b *testing.B) {
	data := makeTestSlice(24)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := clone(data)
		gosort.Slice(buf, func(i, j int) bool {
			return bytes.Compare(buf[i][0], buf[j][0]) < 0
		})
	}
}

func BenchmarkSortKV_StdSort(b *testing.B) {
	data := makeTestSlice(24)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := clone(data)
		gosort.Sort(kvSliceWrapper(buf))
	}
}

type kvSliceWrapper [][2][]byte

func (k kvSliceWrapper) Len() int           { return len(k) }
func (k kvSliceWrapper) Less(i, j int) bool { return bytes.Compare(k[i][0], k[j][0]) < 0 }
func (k kvSliceWrapper) Swap(i, j int)      { k[i], k[j] = k[j], k[i] }
