package lfu

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/buffer"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	"time"
)

const (
	bufferSize    = 1 << 16 // 32MB ring buffer
	sketchWidth   = 1 << 15
	sketchDepth   = 5
	doorkeeperCap = 1 << 18
)

// TinyLFU ties TinyLFU logic into LRU.
type TinyLFU struct {
	ctx        context.Context
	buf        *buffer.Ring
	sketch     *countMinSketch
	doorkeeper *doorkeeper
}

func NewTinyLFU(ctx context.Context) *TinyLFU {
	a := &TinyLFU{
		ctx:        ctx,
		buf:        buffer.NewRingBuffer(bufferSize),
		sketch:     newCountMinSketch(),
		doorkeeper: newDoorkeeper(doorkeeperCap),
	}
	go a.runTinyLFURunner()
	return a
}

func (t *TinyLFU) runTinyLFURunner() {
	ticker := time.NewTicker(time.Millisecond * 500)
	defer ticker.Stop()
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
			for _, key := range t.buf.Snapshot() {
				t.sketch.Increment(key)
			}
		}
	}
}

func (t *TinyLFU) Increment(key uint64) {
	t.sketch.Increment(key)
	t.doorkeeper.Allow(key)
}

func (t *TinyLFU) Admit(new, evict *model.Response) bool {
	kNew := new.Request().MapKey()
	kOld := evict.Request().MapKey()

	// push to getBuf
	t.buf.Push(kNew)

	// doorkeeper check
	if !t.doorkeeper.Allow(kNew) {
		return true // let through only once
	}

	// estimate frequency
	newFreq := t.sketch.Estimate(kNew)
	evictFreq := t.sketch.Estimate(kOld)
	return newFreq >= evictFreq
}

// simple xor-based hash function for sketches.
func hash64(seed, key uint64) uint64 {
	x := key ^ seed
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}
