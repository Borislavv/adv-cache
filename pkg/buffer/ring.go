package buffer

import (
	"sync/atomic"
)

// RingBuffer is a lock-free MPSC ring buffer for uint64.
// Many producers, single consumer.
type RingBuffer struct {
	buffer []uint64
	mask   uint64
	_pad0  [56]byte // prevent false sharing

	head  uint64 // producer position
	_pad1 [56]byte

	tail  uint64 // consumer position
	_pad2 [56]byte
}

// NewRingBuffer creates a new MPSC ring buffer with size = power of 2.
func NewRingBuffer(size int) *RingBuffer {
	if size&(size-1) != 0 {
		panic("size must be a power of 2")
	}
	return &RingBuffer{
		buffer: make([]uint64, size),
		mask:   uint64(size - 1),
	}
}

// Push inserts one item. Returns false if the buffer is full.
func (r *RingBuffer) Push(val uint64) bool {
	for {
		head := atomic.LoadUint64(&r.head)
		tail := atomic.LoadUint64(&r.tail)

		if head-tail >= uint64(len(r.buffer)) {
			return false // buffer full
		}

		if atomic.CompareAndSwapUint64(&r.head, head, head+1) {
			// Write the value after claiming the slot.
			slot := head & r.mask
			atomic.StoreUint64(&r.buffer[slot], val)
			return true
		}
	}
}

// Pop removes one item. Returns false if buffer is empty.
func (r *RingBuffer) Pop() (uint64, bool) {
	tail := atomic.LoadUint64(&r.tail)
	head := atomic.LoadUint64(&r.head)

	if tail == head {
		return 0, false // empty
	}

	val := atomic.LoadUint64(&r.buffer[tail&r.mask])
	atomic.StoreUint64(&r.tail, tail+1)
	return val, true
}

// Drain pops up to max items in batch.
func (r *RingBuffer) Drain(max int) []uint64 {
	out := make([]uint64, 0, max)

	for i := 0; i < max; i++ {
		tail := atomic.LoadUint64(&r.tail)
		head := atomic.LoadUint64(&r.head)

		if tail == head {
			break // empty
		}

		val := atomic.LoadUint64(&r.buffer[tail&r.mask])
		out = append(out, val)
		atomic.StoreUint64(&r.tail, tail+1)
	}

	return out
}

// Len returns approximate number of items.
func (r *RingBuffer) Len() int {
	head := atomic.LoadUint64(&r.head)
	tail := atomic.LoadUint64(&r.tail)
	return int(head - tail)
}
