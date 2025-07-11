package lfu

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/buffer"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"time"
)

const (
	ringBufferCapacity = 1 << 20
	doorkeeperCapacity = 1 << 21
	drainBatchSize     = ringBufferCapacity / 100 * 5 // 5% of whole ring buffer capacity
	// max throughput:
	//	need    -> max 150.000RPS -> 10 ticks per second then 150.000 / 10 = 15.000 (batch drain per iter)
	//  current -> max 524.250RPS -> 52425 (batch drain per iter) * 10 (ticks per second)
)

type TinyLFU struct {
	ring       *buffer.RingBuffer
	sketch     *countMinSketch
	doorkeeper *doorkeeper
}

func NewTinyLFU(ctx context.Context) *TinyLFU {
	t := &TinyLFU{
		sketch:     newCountMinSketch(),
		doorkeeper: newDoorkeeper(doorkeeperCapacity),
		ring:       buffer.NewRingBuffer(ringBufferCapacity),
	}
	go t.run(ctx)
	return t
}

func (t *TinyLFU) run(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, k := range t.ring.Drain(drainBatchSize) {
				t.sketch.Increment(k)
			}
		}
	}
}

func (t *TinyLFU) Increment(key uint64) {
	t.ring.Push(key)
	t.doorkeeper.Allow(key)
}

func (t *TinyLFU) Admit(new, old *model.Entry) bool {
	newKey := new.MapKey()
	oldKey := old.MapKey()

	if !t.doorkeeper.Allow(newKey) {
		return true
	}

	newFreq := t.sketch.Estimate(newKey)
	victimFreq := t.sketch.Estimate(oldKey)

	return newFreq >= victimFreq
}
