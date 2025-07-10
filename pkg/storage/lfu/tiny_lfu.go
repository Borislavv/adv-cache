package lfu

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/buffer"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"time"
)

type TinyLFU struct {
	ring       *buffer.RingBuffer
	sketch     *countMinSketch
	doorkeeper *doorkeeper
}

func NewTinyLFU(ctx context.Context) *TinyLFU {
	t := &TinyLFU{
		ring:       buffer.NewRingBuffer(1 << 16),
		sketch:     newCountMinSketch(),
		doorkeeper: newDoorkeeper(1 << 18),
	}
	go t.run(ctx)
	return t
}

func (t *TinyLFU) run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			items := t.ring.Drain(1000)
			for _, k := range items {
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
