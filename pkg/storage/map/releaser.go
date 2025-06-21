package sharded

import (
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/consts"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/synced"
	"unsafe"
)

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
	val.IncRefCount()
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
