package sharded

import (
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/consts"
	synced "github.com/Borislavv/traefik-http-cache-plugin/pkg/sync"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Shard is a single partition of the sharded map.
// Each shard is an independent concurrent map with its own lock and refCounted pool for releasers.
type Shard[V Value] struct {
	*sync.RWMutex                                 // Shard-level RWMutex for concurrency
	items         map[uint64]V                    // Actual storage: key -> Value
	releaserPool  *synced.BatchPool[*Releaser[V]] // Pool for recycling Releaser objects (to minimize allocations)
	id            uint64                          // Shard ID (index)
	mem           int64                           // Weight usage in bytes (atomic)
}

// Releaser is a wrapper for refCounting and releasing cached values.
// Returned by Set and Get; must be released when no longer needed.
type Releaser[V Value] struct {
	val   V                               // Value being tracked
	relFn func(v V) bool                  // Release function: decrements refCount, may free value
	pool  *synced.BatchPool[*Releaser[V]] // Pool for recycling this object
}

func (r *Releaser[V]) Weight() int64 {
	return int64(unsafe.Sizeof(*r)) + consts.PtrBytesWeight
}

// NewReleaser returns a pooled Releaser for a value, with a custom release logic.
func NewReleaser[V Value](val V, pool *synced.BatchPool[*Releaser[V]]) *Releaser[V] {
	rel := pool.Get()
	*rel = Releaser[V]{
		val:  val,
		pool: pool,
		relFn: func(value V) bool {
			// Atomically decrement refCount. If the value is doomed and refCount drops to zero, actually release it.
			if old := value.RefCount(); value.CASRefCount(old, old-1) {
				if old == 1 && value.IsDoomed() {
					value.Release()
				}
				return true
			}
			return false
		},
	}
	return rel
}

// Release decrements the refCount and recycles the Releaser itself.
func (r *Releaser[V]) Release() bool {
	if r == nil {
		return true
	}
	ok := r.relFn(r.val)
	if ok {
		r.pool.Put(r)
		return true
	}
	return false
}

// NewShard creates a new shard with its own lock, value map, and releaser pool.
func NewShard[V Value](id uint64, defaultLen int) *Shard[V] {
	return &Shard[V]{
		id:      id,
		RWMutex: &sync.RWMutex{},
		items:   make(map[uint64]V, defaultLen),
		releaserPool: synced.NewBatchPool[*Releaser[V]](synced.PreallocateBatchSize, func() *Releaser[V] {
			return new(Releaser[V])
		}),
	}
}

// ID returns the numeric index of this shard.
func (shard *Shard[V]) ID() uint64 {
	return shard.id
}

// Weight returns an approximate total memory usage for this shard (including overhead).
func (shard *Shard[V]) Weight() int64 {
	return atomic.LoadInt64(&shard.mem)
}

// Set inserts or updates a value by key, resets refCount, and updates counters.
// Returns a releaser for the inserted value.
func (shard *Shard[V]) Set(key uint64, value V) (takenMem int64, releaser *Releaser[V]) {
	value.StoreRefCount(1)

	shard.Lock()
	shard.items[key] = value
	shard.Unlock()

	takenMem = value.Weight()

	atomic.AddInt64(&shard.mem, takenMem)

	// Return a releaser for this value (for the user to release later).
	return takenMem, NewReleaser(value, shard.releaserPool)
}

// Get retrieves a value and returns a releaser for it, incrementing its refCount.
// Returns (value, releaser, true) if found; otherwise (zero, nil, false).
func (shard *Shard[V]) Get(key uint64) (val V, releaser *Releaser[V], isHit bool) {
	shard.RLock()
	value, ok := shard.items[key]
	shard.RUnlock()
	if ok {
		value.IncRefCount()
		return value, NewReleaser(value, shard.releaserPool), true
	}
	return value, nil, false
}

// Release removes a value from the shard, decrements counters, and may trigger full resource cleanup.
// Returns (memory_freed, pointer_to_list_element, was_found).
func (shard *Shard[V]) Release(key uint64) (freed int64, isHit bool) {
	shard.Lock()
	v, ok := shard.items[key]
	if ok {
		delete(shard.items, key)
		shard.Unlock()

		weight := v.Weight()
		atomic.AddInt64(&shard.mem, -weight)

		// If all references are gone, call Release; otherwise mark as doomed for future cleanup.
		if v.MarkAsDoomed() && v.RefCount() == 0 {
			v.Release()
		}

		return weight, true
	}
	shard.Unlock()

	return 0, false
}
