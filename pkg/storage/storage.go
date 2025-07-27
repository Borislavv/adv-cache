package storage

import (
	"github.com/Borislavv/advanced-cache/pkg/model"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
)

// Storage is a generic interface for cache storages.
// It supports typical Get/Set operations with reference management.
type Storage interface {
	// Get attempts to retrieve a cached response for the given request.
	// Returns the response, a releaser for safe concurrent access, and a hit/miss flag.
	Get(*model.Entry) (entry *model.VersionPointer, found bool)

	// Set stores a new response in the cache and returns a releaser for managing resource lifetime.
	// 1. You definitely cannot use 'request' after use in Set due to it can be removed, you will receive a cache entry on hit!
	Set(request *model.VersionPointer) (entry *model.VersionPointer, persisted bool)

	// Remove is removes one element.
	Remove(*model.VersionPointer)

	// Clear is removes all cache entries from the storage.
	Clear()

	// Stat returns bytes usage and num of items in storage.
	Stat() (bytes int64, length int64)

	// Len - return stored value (refreshes every 100ms).
	Len() int64

	// Mem - return stored value (refreshes every 100ms).
	Mem() int64

	// RealMem - calculates and return value.
	RealMem() int64

	// Rand returns a random elem from the map.
	Rand() (entry *model.VersionPointer, ok bool)

	WalkShards(fn func(key uint64, shard *sharded.Shard[*model.VersionPointer]))
}
