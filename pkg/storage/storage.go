package storage

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/storage/lfu"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	"runtime"
	"strconv"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/rs/zerolog/log"
)

// Storage is a generic interface for cache storages.
// It supports typical Get/Push operations with reference management.
type Storage interface {
	// Get attempts to retrieve a cached response for the given request.
	// Returns the response, a releaser for safe concurrent access, and a hit/miss flag.
	Get(*model.Entry) (entry *model.VersionPointer, ok bool)

	// Set stores a new response in the cache and returns a releaser for managing resource lifetime.
	// 1. You definitely cannot use 'request' after use in Set due to it can be removed, you will receive a cache entry on hit!
	Set(new *model.VersionPointer) (entry *model.VersionPointer, wasPersisted bool)

	// Remove is removes one element.
	Remove(*model.VersionPointer) (freedBytes int64, isHit bool)

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

// InMemoryStorage is a Weight-aware, sharded LRU in-memory cache with background eviction and refreshItem support.
type InMemoryStorage struct {
	ctx             context.Context                     // Main context for lifecycle control
	cfg             *config.Cache                       // CacheBox configuration
	shardedMap      *sharded.Map[*model.VersionPointer] // Sharded storage for cache entries
	tinyLFU         *lfu.TinyLFU                        // Helps hold more frequency used items in cache while eviction
	backend         repository.Backender                // Remote backend server.
	balancer        lru.Balancer                        // Helps pick shards to evict from
	mem             int64                               // Current Weight usage (bytes)
	memoryThreshold int64                               // Threshold for triggering eviction (bytes)
}

// NewStorage constructs a new InMemoryStorage cache instance and launches eviction and refreshItem routines.
func NewStorage(ctx context.Context, cfg *config.Cache, backend repository.Backender) *InMemoryStorage {
	shardedMap := sharded.NewMap[*model.VersionPointer](ctx, cfg.Cache.Preallocate.PerShard)
	balancer := lru.NewBalancer(ctx, shardedMap)

	db := (&InMemoryStorage{
		ctx:             ctx,
		cfg:             cfg,
		shardedMap:      shardedMap,
		balancer:        balancer,
		backend:         backend,
		tinyLFU:         lfu.NewTinyLFU(ctx),
		memoryThreshold: int64(float64(cfg.Cache.Storage.Size) * cfg.Cache.Eviction.Threshold),
	}).init().runLogger()

	NewEvictor(ctx, cfg, db, balancer).Run()
	NewRefresher(ctx, cfg, db).Run()

	return db
}

func (s *InMemoryStorage) init() *InMemoryStorage {
	// Register all existing shards with the balancer.
	s.shardedMap.WalkShards(func(shardKey uint64, shard *sharded.Shard[*model.VersionPointer]) {
		s.balancer.Register(shard)
	})

	return s
}

func (s *InMemoryStorage) Clear() {
	s.shardedMap.WalkShards(func(key uint64, shard *sharded.Shard[*model.VersionPointer]) {
		shard.Clear()
	})
}

// Get retrieves a response by request and bumps its InMemoryStorage position.
// Returns: (response, releaser, found).
func (s *InMemoryStorage) Get(req *model.Entry) (entry *model.VersionPointer, ok bool) {
	if entry, ok = s.shardedMap.Get(req.MapKey()); ok && entry.IsSameFingerprint(req.Fingerprint()) {
		s.touch(entry)
		return entry, true
	}
	return nil, false
}

// Rand returns a random item from storage.
func (s *InMemoryStorage) Rand() (entry *model.VersionPointer, ok bool) {
	return s.shardedMap.Rnd()
}

// Set inserts or updates a response in the cache, updating Weight usage and InMemoryStorage position.
// On 'wasPersisted=true' must be called Entry.Finalize, otherwise Entry.Finalize.
func (s *InMemoryStorage) Set(new *model.VersionPointer) (entry *model.VersionPointer, wasPersisted bool) {
	// Track access frequency
	s.tinyLFU.Increment(new.MapKey())

	// Attempt to find entry in cache, if found then touch and return it
	if old, found := s.shardedMap.Get(new.MapKey()); found {
		if old.IsSameFingerprint(new.Fingerprint()) {
			// keys matched totally (collision are excluded)

			if old.IsSamePayload(new.Entry) {
				s.touch(old)
			} else {
				s.update(old, new)
			}

			new.Remove()
			return old, true
		} else {
			// hash collision case (rewrite entry)
			old.Remove()
		}
	}

	// Admission control: if memory is over threshold, evaluate before inserting
	if s.ShouldEvict() {
		if victim, found := s.balancer.FindVictim(new.ShardKey()); !found || !s.tinyLFU.Admit(new, victim) {
			// Cases that answer the question: why are we here?
			// 1. Victim not found (cannot write more than the given memory limit)
			// 2. New element is rarer than victim, skip insertion
			return new, false
		}
	}

	s.set(new)
	return new, true
}

func (s *InMemoryStorage) Remove(entry *model.VersionPointer) (freedBytes int64, isHit bool) {
	return s.shardedMap.Remove(entry.MapKey())
}

func (s *InMemoryStorage) Len() int64 {
	return s.shardedMap.Len()
}

func (s *InMemoryStorage) Mem() int64 {
	return s.shardedMap.Mem() + s.balancer.Mem()
}

func (s *InMemoryStorage) RealMem() int64 {
	return s.shardedMap.RealMem()
}

func (s *InMemoryStorage) Stat() (bytes int64, length int64) {
	return s.shardedMap.Mem(), s.shardedMap.Len()
}

// ShouldEvict [HOT PATH METHOD] (max stale value = 25ms) checks if current Weight usage has reached or exceeded the threshold.
func (s *InMemoryStorage) ShouldEvict() bool {
	return s.Mem() >= s.memoryThreshold
}

func (s *InMemoryStorage) WalkShards(fn func(key uint64, shard *sharded.Shard[*model.VersionPointer])) {
	s.shardedMap.WalkShards(fn)
}

// touch bumps the InMemoryStorage position of an existing entry (MoveToFront) and increases its refCount.
func (s *InMemoryStorage) touch(existing *model.VersionPointer) {
	s.balancer.Update(existing)
}

// update refreshes Weight accounting and InMemoryStorage position for an updated entry.
func (s *InMemoryStorage) update(existing, new *model.VersionPointer) {
	existing.SwapPayloads(new.Entry)
	existing.TouchUpdatedAt()
	s.balancer.Update(existing)
}

// set inserts a new response, updates Weight usage and registers in balancer.
func (s *InMemoryStorage) set(new *model.VersionPointer) (ok bool) {
	if ok = s.shardedMap.Set(new.MapKey(), new); ok {
		s.balancer.Push(new)
	}
	return ok
}

// runLogger emits detailed stats about evictions, Weight, and GC activity every 5 seconds if debugging is enabled.
func (s *InMemoryStorage) runLogger() *InMemoryStorage {
	go func() {
		var ticker = utils.NewTicker(s.ctx, 5*time.Second)

		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)

				var (
					realMem    = s.shardedMap.Mem()
					mem        = utils.FmtMem(realMem)
					length     = strconv.Itoa(int(s.shardedMap.Len()))
					gc         = strconv.Itoa(int(m.NumGC))
					limit      = utils.FmtMem(int64(s.cfg.Cache.Storage.Size))
					goroutines = strconv.Itoa(runtime.NumGoroutine())
					alloc      = utils.FmtMem(int64(m.Alloc))
				)

				logEvent := log.Info()

				if s.cfg.IsProd() {
					logEvent.
						Str("target", "storage").
						Str("mem", strconv.Itoa(int(realMem))).
						Str("memStr", mem).
						Str("len", length).
						Str("gc", gc).
						Str("memLimit", strconv.Itoa(int(s.cfg.Cache.Storage.Size))).
						Str("memLimitStr", limit).
						Str("goroutines", goroutines).
						Str("alloc", strconv.Itoa(int(m.Alloc))).
						Str("allocStr", alloc)
				}

				logEvent.Msgf("[storage][5s] usage: %s, len: %s, limit: %s, alloc: %s, goroutines: %s, gc: %s",
					mem, length, limit, alloc, goroutines, gc)
			}
		}
	}()
	return s
}
