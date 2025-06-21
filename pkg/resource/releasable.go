package resource

// Releasable defines reference-counted resource management for cache values.
type Releasable interface {
	Release() bool
	RefCount() int64
	IncRefCount() int64
	DecRefCount() int64
	CASRefCount(old, new int64) bool
	StoreRefCount(new int64)
	IsDoomed() bool
	MarkAsDoomed() bool
	ShardListElement() any // Used for pointer to the element in the LRU or similar
}
