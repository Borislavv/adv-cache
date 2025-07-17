package sharded

import (
	"sync"
)

// Releaser is a wrapper for refCounting and releasing cached values.
// Returned by Set and Get; must be released when no longer needed.
type Releaser[V Value] struct { // Value being tracked
	val  V
	pool *sync.Pool // Pool for recycling this object
}

// newReleaser returns a pooled Releaser for a value, with a custom release logic.
func newReleaser[V Value](val V, pool *sync.Pool) *Releaser[V] {
	rel := pool.Get().(*Releaser[V])
	*rel = Releaser[V]{
		pool: pool,
		val:  val,
	}
	return rel
}

// Release decrements the refCount and recycles the Releaser itself.
func (r *Releaser[V]) Release() {
	if r != nil {
		r.val.Release()
		var v V
		r.val = v
		r.pool.Put(r)
		return
	}
}
