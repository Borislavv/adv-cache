package storage

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/config"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/repository"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/cache"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/cache/lfu"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/cache/lru"
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
)

// Storage is a generic interface for cache storages.
// It supports typical Get/Set operations with reference management.
type Storage interface {
	// Get attempts to retrieve a cached response for the given request.
	// Returns the response, a releaser for safe concurrent access, and a hit/miss flag.
	Get(req *model.Request) (resp *model.Response, isHit bool)

	// Set stores a new response in the cache and returns a releaser for managing resource lifetime.
	Set(resp *model.Response)

	// Stop dumps itself in FS.
	Stop()
}

// AlgoStorage is a wrapper that delegates actual storage logic to an underlying algorithm implementation.
type AlgoStorage struct {
	Storage
}

// New returns a new instance of AlgoStorage,
// initializing the appropriate cache eviction algorithm according to configuration.
//
// Params:
//
//	ctx         - context for cancellation and control
//	cfg         - cache configuration (eviction algorithm, memory limit, etc.)
//	shardedMap  - shared sharded map storage for concurrent key/value access
func New(
	ctx context.Context,
	cfg *config.Cache,
	balancer lru.Balancer,
	refresher lru.Refresher,
	backend repository.Backender,
	tinyLFU *lfu.TinyLFU,
	shardedMap *sharded.Map[*model.Response],
) (db *AlgoStorage) {
	var s Storage

	// TODO need implement select by storage type (malloc and so on)

	// Select and initialize storage backend by eviction algorithm type.
	switch cache.Algorithm(cfg.Cache.Eviction.Policy) {
	case cache.LRU:
		// Least Recently Used (Storage) cache
		s = lru.NewStorage(ctx, cfg, balancer, refresher, backend, tinyLFU, shardedMap)
	default:
		// Panic for unsupported/unknown algorithms.
		panic("eviction policy '" + cfg.Cache.Eviction.Policy + "' is not implemented yet")
	}

	return &AlgoStorage{Storage: s}
}
