package buffer

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRingBuffer_Basic(t *testing.T) {
	rb := NewRingBuffer(8)

	if ok := rb.Push(42); !ok {
		t.Fatal("Push failed unexpectedly")
	}

	val, ok := rb.Pop()
	if !ok {
		t.Fatal("Pop failed unexpectedly")
	}
	if val != 42 {
		t.Fatalf("Expected 42, got %d", val)
	}

	// Pop on empty
	_, ok = rb.Pop()
	if ok {
		t.Fatal("Pop should fail on empty buffer")
	}
}

func TestRingBuffer_FullAndEmpty(t *testing.T) {
	rb := NewRingBuffer(4)

	// Fill it
	for i := 0; i < 4; i++ {
		if !rb.Push(uint64(i)) {
			t.Fatal("Push failed before full")
		}
	}

	// Should be full now
	if rb.Push(100) {
		t.Fatal("Push should fail when full")
	}

	// Drain it
	for i := 0; i < 4; i++ {
		v, ok := rb.Pop()
		if !ok || int(v) != i {
			t.Fatalf("Unexpected value: %d", v)
		}
	}

	// Should be empty now
	_, ok := rb.Pop()
	if ok {
		t.Fatal("Pop should fail when empty")
	}
}

func TestRingBuffer_Drain(t *testing.T) {
	rb := NewRingBuffer(8)
	for i := 0; i < 5; i++ {
		rb.Push(uint64(i))
	}

	drained := rb.Drain(3)
	if len(drained) != 3 {
		t.Fatalf("Expected 3 drained, got %d", len(drained))
	}
	for i, v := range drained {
		if int(v) != i {
			t.Fatalf("Unexpected drained value %d at %d", v, i)
		}
	}

	drained = rb.Drain(10)
	if len(drained) != 2 {
		t.Fatalf("Expected 2 drained, got %d", len(drained))
	}

	// Should be empty now
	drained = rb.Drain(1)
	if len(drained) != 0 {
		t.Fatal("Drain on empty should return empty slice")
	}
}

func TestRingBuffer_ParallelProducersSingleConsumer(t *testing.T) {
	rb := NewRingBuffer(1 << 10) // 1024 slots

	const producers = 8
	const itemsPerProducer = 100_000

	var pushed uint64
	var popped uint64

	wg := sync.WaitGroup{}
	wg.Add(producers)

	// Producers
	for i := 0; i < producers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < itemsPerProducer; {
				ok := rb.Push(uint64(rand.Int()))
				if ok {
					atomic.AddUint64(&pushed, 1)
					j++
				}
			}
		}(i)
	}

	// Single consumer
	done := make(chan struct{})
	go func() {
		for atomic.LoadUint64(&pushed) < uint64(producers*itemsPerProducer) ||
			atomic.LoadUint64(&popped) < atomic.LoadUint64(&pushed) {
			val, ok := rb.Pop()
			if ok {
				_ = val // simulate processing
				atomic.AddUint64(&popped, 1)
			} else {
				time.Sleep(time.Microsecond) // yield
			}
		}
		close(done)
	}()

	wg.Wait()
	<-done

	if pushed != popped {
		t.Errorf("Mismatch: pushed=%d popped=%d", pushed, popped)
	}
}

func BenchmarkRingBuffer_PushPop(b *testing.B) {
	rb := NewRingBuffer(1 << 12)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !rb.Push(uint64(i)) {
			b.Fatal("Push failed unexpectedly")
		}
		_, ok := rb.Pop()
		if !ok {
			b.Fatal("Pop failed unexpectedly")
		}
	}
}
