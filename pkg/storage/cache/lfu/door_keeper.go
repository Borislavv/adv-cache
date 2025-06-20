package lfu

import "math/rand/v2"

// doorkeeper is a simple Bloom filter.
type doorkeeper struct {
	bits  []uint64
	seeds [2]uint64
}

func newDoorkeeper(capacity int) *doorkeeper {
	return &doorkeeper{
		bits:  make([]uint64, capacity/64),
		seeds: [2]uint64{rand.Uint64(), rand.Uint64()},
	}
}

func (d *doorkeeper) Allow(key uint64) bool {
	h1 := hash64(d.seeds[0], key)
	h2 := hash64(d.seeds[1], key)
	p1 := h1 % uint64(len(d.bits)*64)
	p2 := h2 % uint64(len(d.bits)*64)
	b1 := (d.bits[p1/64] & (1 << (p1 % 64))) != 0
	b2 := (d.bits[p2/64] & (1 << (p2 % 64))) != 0
	if b1 && b2 {
		return true
	}
	d.bits[p1/64] |= 1 << (p1 % 64)
	d.bits[p2/64] |= 1 << (p2 % 64)
	return false
}
