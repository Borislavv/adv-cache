package buffer

import "sync/atomic"

// Ring is a lock-free circular buffer for recording access keys.
type Ring struct {
	buffer []uint64
	mask   uint64
	pos    uint64 // atomic
}

func NewRingBuffer(size int) *Ring {
	if size&(size-1) != 0 {
		panic("ring buffer size must be power of 2")
	}
	return &Ring{
		buffer: make([]uint64, size),
		mask:   uint64(size - 1),
	}
}

func (r *Ring) Push(key uint64) {
	pos := atomic.AddUint64(&r.pos, 1) - 1
	r.buffer[pos&r.mask] = key
}

func (r *Ring) Snapshot() []uint64 {
	buf := make([]uint64, len(r.buffer))
	copy(buf, r.buffer)
	return buf
}
