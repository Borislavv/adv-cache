package test

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type Entry struct {
	payload    atomic.Pointer[[]byte]
	refCount   int64  // <0 — finalized; >=0 — ref counter
	isDoomed   int64  // atomic bool flag
	generation uint64 // increments on each finalize()
}

var entryPool = sync.Pool{
	New: func() any {
		return &Entry{}
	},
}

func NewEntry() *Entry {
	e := entryPool.Get().(*Entry)
	var payload = []byte("data")
	e.payload.Store(&payload)
	atomic.StoreInt64(&e.refCount, 0)
	atomic.StoreInt64(&e.isDoomed, 0)
	// NOTE: generation remains from previous lifecycle
	return e
}

func (e *Entry) Acquire(expectedGen uint64) bool {
	if atomic.LoadUint64(&e.generation) != expectedGen || atomic.LoadInt64(&e.refCount) < 0 {
		return false
	}
	for {
		old := atomic.LoadInt64(&e.refCount)
		if old < 0 {
			return false
		}
		if atomic.CompareAndSwapInt64(&e.refCount, old, old+1) {
			return true
		}
	}
}

func (e *Entry) Release() bool {
	for {
		old := atomic.LoadInt64(&e.refCount)
		if old <= 0 {
			return false // double release or already finalized
		}
		if atomic.CompareAndSwapInt64(&e.refCount, old, old-1) {
			if old == 1 && atomic.LoadInt64(&e.isDoomed) == 1 {
				if atomic.CompareAndSwapInt64(&e.refCount, 0, -1) {
					e.finalize()
					return true
				}
			}
			return false
		}
	}
}

func (e *Entry) Remove() bool {
	if atomic.CompareAndSwapInt64(&e.isDoomed, 0, 1) {
		return e.Release()
	}
	return false
}

func (e *Entry) finalize() {
	e.payload.Store(nil)
	atomic.AddUint64(&e.generation, 1)
	atomic.StoreInt64(&e.isDoomed, 0)
	entryPool.Put(e)
}

// TaggedEntry — used to detect ABA
type TaggedEntry struct {
	Ptr *Entry
	Gen uint64
}

func TestRefCounting(t *testing.T) {
	var (
		db   = make(map[int]TaggedEntry)
		mu   sync.Mutex
		done = make(chan struct{})
	)

	for i := 0; i < 150; i++ {
		e := NewEntry()
		db[i] = TaggedEntry{Ptr: e, Gen: atomic.LoadUint64(&e.generation)}
	}

	go func() {
		time.Sleep(5 * time.Second)
		close(done)
	}()

	// Readers
	for i := 0; i < 5; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					idx := rand.Intn(150)
					mu.Lock()
					te := db[idx]
					mu.Unlock()
					if te.Ptr.Acquire(te.Gen) {
						payload := te.Ptr.payload.Load()
						if payload == nil {
							panic("payload is nil")
						}
						te.Ptr.Release()
					}
				}
			}
		}()
	}

	// Writers
	for i := 0; i < 10; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					idx := rand.Intn(150)
					mu.Lock()
					old := db[idx]
					delete(db, idx)
					if old.Ptr.Acquire(old.Gen) {
						_ = old.Ptr.Remove()
					}
					e := NewEntry()
					db[idx] = TaggedEntry{Ptr: e, Gen: atomic.LoadUint64(&e.generation)}
					mu.Unlock()
				}
			}
		}()
	}

	time.Sleep(6 * time.Second)
}
