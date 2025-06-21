package sharded

import (
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/consts"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/resource"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/synced"
	"unsafe"
)

// Releaser is a wrapper for refCounting and releasing cached values.
// Returned by Set and Get; must be released when no longer needed.
type Releaser[V resource.Resource] struct {
	val   V                                      // Resource being tracked
	relFn func(v V) (freedMem int64, isHit bool) // Release function: decrements refCount, may free value
	pool  *synced.BatchPool[*Releaser[V]]        // Pool for recycling this object
}

func (r *Releaser[V]) Weight() int64 {
	return int64(unsafe.Sizeof(*r)) + consts.PtrBytesWeight
}

// NewReleaser returns a pooled Releaser for a value, with a custom release logic.
func NewReleaser[V resource.Resource](val V, pool *synced.BatchPool[*Releaser[V]]) *Releaser[V] {
	val.IncRefCount()
	rel := pool.Get()
	*rel = Releaser[V]{
		val:  val,
		pool: pool,
		relFn: func(value V) (int64, bool) {
			for {
				// Atomically decrement refCount. If the value is doomed and refCount drops to zero, actually release it.
				if old := value.RefCount(); value.CASRefCount(old, old-1) {
					if value.IsDoomed() && old == 1 {
						weight := value.Weight()
						value.Release()
						return weight, true
					}
					return 0, false
				} else {
					continue
				}
			}
		},
	}
	return rel
}

// Release decrements the refCount and recycles the Releaser itself.
func (r *Releaser[V]) Release() (freedBytes int64, isHit bool) {
	if r == nil {
		return 0, false
	}
	freedBytes, isHit = r.relFn(r.val)
	if isHit {
		r.pool.Put(r)
		return freedBytes, isHit
	}
	return 0, false
}
