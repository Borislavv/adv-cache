package cache

// Algorithm is the string type for caching algorithms labels.
type Algorithm string

const (
	// LRU is the constant for Least Recently Used.
	LRU Algorithm = "lru"

	// MRU is the constant for Most Recently Used.
	MRU Algorithm = "mru"

	// LFU is the constant for Least Frequently Used.
	LFU Algorithm = "lfu"

	// MFU is the constant for Most Frequently Used.
	MFU Algorithm = "mfu"
)
