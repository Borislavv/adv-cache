package lfu

import "math/rand/v2"

// countMinSketch is a probabilistic frequency counter (used for admission).
type countMinSketch struct {
	table [sketchDepth][sketchWidth]uint8
	seeds [sketchDepth]uint64
}

func newCountMinSketch() *countMinSketch {
	c := &countMinSketch{}
	for i := 0; i < sketchDepth; i++ {
		c.seeds[i] = rand.Uint64()
	}
	return c
}

func (c *countMinSketch) Increment(key uint64) {
	for i := 0; i < sketchDepth; i++ {
		h := hash64(c.seeds[i], key)
		c.table[i][h%sketchWidth]++
	}
}

func (c *countMinSketch) Estimate(key uint64) uint8 {
	minimum := uint8(255)
	for i := 0; i < sketchDepth; i++ {
		h := hash64(c.seeds[i], key)
		v := c.table[i][h%sketchWidth]
		if v < minimum {
			minimum = v
		}
	}
	return minimum
}
