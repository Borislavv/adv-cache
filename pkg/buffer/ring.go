package buffer

import (
	"sync/atomic"
)

type RingBuffer struct {
	buf      []uint64
	mask     uint64
	head     uint64 // atomic
	tail     uint64 // atomic
	capacity uint64
}

func NewRingBuffer(size int) *RingBuffer {
	// size must be power of 2
	r := &RingBuffer{
		buf:      make([]uint64, size),
		capacity: uint64(size),
		mask:     uint64(size - 1),
	}
	return r
}

func (r *RingBuffer) Push(key uint64) bool {
	head := atomic.LoadUint64(&r.head)
	tail := atomic.LoadUint64(&r.tail)
	if head-tail >= r.capacity {
		return false // buffer full
	}
	idx := head & r.mask
	r.buf[idx] = key
	atomic.AddUint64(&r.head, 1)
	return true
}

func (r *RingBuffer) Drain(max int) []uint64 {
	tail := atomic.LoadUint64(&r.tail)
	head := atomic.LoadUint64(&r.head)

	n := head - tail
	if n == 0 {
		return nil
	}
	if n > uint64(max) {
		n = uint64(max)
	}

	result := make([]uint64, 0, n)
	for i := uint64(0); i < n; i++ {
		idx := (tail + i) & r.mask
		result = append(result, r.buf[idx])
	}
	atomic.AddUint64(&r.tail, n)
	return result
}
