package lfu

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/buffer"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"math/rand"
	"testing"
	"time"
)

func BenchmarkTinyLFUIncrement(b *testing.B) {
	tlfu := NewTinyLFU(context.Background())

	keys := make([]uint64, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = rand.Uint64()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tlfu.Increment(keys[i])
	}
}

func BenchmarkTinyLFUAdmit(b *testing.B) {
	tlfu := NewTinyLFU(context.Background())

	// simulate some initial frequencies
	for i := 0; i < 100000; i++ {
		tlfu.Increment(uint64(i))
	}
	time.Sleep(time.Second) // wait for run()

	newEntry := (&model.Entry{}).SetMapKey(rand.Uint64())
	oldEntry := (&model.Entry{}).SetMapKey(rand.Uint64())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tlfu.Admit(newEntry, oldEntry)
	}
}

func BenchmarkRingBufferPush(b *testing.B) {
	rb := buffer.NewRingBuffer(1 << 16)
	keys := make([]uint64, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = rand.Uint64()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rb.Push(keys[i])
	}
}

func BenchmarkRingBufferDrain(b *testing.B) {
	rb := buffer.NewRingBuffer(1 << 16)
	for i := 0; i < 100000; i++ {
		rb.Push(uint64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rb.Drain(1000)
	}
}
