package pool

import (
	"sync"
)

// SizedBytePool manages multiple sync.Pool for different size classes.
type SizedBytePool struct {
	pools map[int]*sync.Pool
	sizes []int
}

// NewSizedBytePool initializes all size classes.
func NewSizedBytePool() *SizedBytePool {
	sizes := []int{
		256, 512, 1024, 2048, 4096,
		8192, 16000, 32000, 64000,
		128000, 256000, 512000, 1000000,
	}

	pools := make(map[int]*sync.Pool, len(sizes))
	for _, size := range sizes {
		sz := size // capture loop var
		pools[sz] = &sync.Pool{
			New: func() any {
				buf := make([]byte, sz)
				return &buf
			},
		}
	}

	return &SizedBytePool{
		pools: pools,
		sizes: sizes,
	}
}

// Get returns a []byte pointer with capacity for the given weight.
func (s *SizedBytePool) Get(weight int) *[]byte {
	size := s.sizeClass(weight)
	pool := s.pools[size]
	bufPtr := pool.Get().(*[]byte)
	*bufPtr = (*bufPtr)[:0] // reset length
	return bufPtr
}

// Put returns the buffer to the appropriate pool.
func (s *SizedBytePool) Put(buf *[]byte) {
	size := s.sizeClass(cap(*buf))
	pool := s.pools[size]
	pool.Put(buf)
}

// sizeClass finds the smallest size class >= weight.
func (s *SizedBytePool) sizeClass(weight int) int {
	for _, size := range s.sizes {
		if weight <= size {
			return size
		}
	}
	return s.sizes[len(s.sizes)-1] // fallback: biggest class
}
