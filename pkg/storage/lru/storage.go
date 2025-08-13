package lru

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/storage/lfu"
	"github.com/Borislavv/advanced-cache/pkg/upstream"
	"runtime"
	"strconv"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/model"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/rs/zerolog/log"
)

// Storage is a generic interface for cache storages.
// It supports typical Get/Set operations with reference management.
type Storage interface {
	Run()
	Close() error

	// Get attempts to retrieve a cached response for the given request.
	// Returns the response, a releaser for safe concurrent access, and a hit/miss flag.
	Get(*model.Entry) (entry *model.Entry, hit bool)

	// Set stores a new response in the cache and returns a releaser for managing resource lifetime.
	// 1. You definitely cannot use 'inEntry' after use in Set due to it can be removed, you will receive a cache entry on hit!
	// 2. Use Release and Remove for manage Entry lifetime.
	Set(inEntry *model.Entry) (persisted bool)

	// Remove is removes one element.
	Remove(*model.Entry) (freedBytes int64, hit bool)

	// Clear is removes all cache entries from the storage.
	Clear()

	// Stat returns bytes usage and num of items in storage.
	Stat() (bytes int64, length int64)

	// Len - return stored value (refreshes every 100ms).
	Len() int64

	RealLen() int64

	// Mem - return stored value (refreshes every 100ms).
	Mem() int64

	// RealMem - calculates and return value.
	RealMem() int64

	// Rand returns a random elem from the map.
	Rand() (entry *model.Entry, ok bool)

	WalkShards(ctx context.Context, fn func(key uint64, shard *sharded.Shard[*model.Entry]))

	// Dumper functionality
	Dump(ctx context.Context) error
	Load(ctx context.Context) error
	LoadVersion(ctx context.Context, v string) error
}

// InMemoryStorage is a Weight-aware, sharded InMemoryStorage cache with background eviction and refreshItem support.
type InMemoryStorage struct {
	ctx             context.Context            // Main context for lifecycle control
	cfg             config.Config              // AtomicCacheBox configuration
	shardedMap      *sharded.Map[*model.Entry] // Sharded storage for cache entries
	tinyLFU         *lfu.TinyLFU               // Helps hold more frequency used items in cache while eviction
	upstream        upstream.Upstream          // Remote upstream server.
	refresher       Refresher
	balancer        Balancer // Helps pick shards to evict from
	evictor         Evictor
	dumper          Dumper
	mem             int64 // Current Weight usage (bytes)
	memoryThreshold int64 // Threshold for triggering eviction (bytes)
}

// NewStorage constructs a new InMemoryStorage cache instance and launches eviction and refreshItem routines.
func NewStorage(ctx context.Context, cfg config.Config, upstream upstream.Upstream) *InMemoryStorage {
	shardedMap := sharded.NewMap[*model.Entry](ctx)
	balancer := NewBalancer(ctx, shardedMap)

	db := (&InMemoryStorage{
		ctx:             ctx,
		cfg:             cfg,
		shardedMap:      shardedMap,
		balancer:        balancer,
		upstream:        upstream,
		tinyLFU:         lfu.NewTinyLFU(ctx),
		memoryThreshold: int64(float64(cfg.Storage().Size) * cfg.Eviction().Threshold),
	}).init()

	db.evictor = NewEvictor(ctx, cfg, db, db.balancer)
	db.refresher = NewRefresher(ctx, cfg, db, upstream)
	db.dumper = NewDumper(cfg, db, upstream)

	return db
}

func (s *InMemoryStorage) init() *InMemoryStorage {
	// Register all existing shards with the balancer.
	s.shardedMap.WalkShards(s.ctx, func(shardKey uint64, shard *sharded.Shard[*model.Entry]) {
		s.balancer.Register(shard)
	})

	return s
}

func (s *InMemoryStorage) Run() {
	s.runLogger()
	s.evictor.Run()
	s.refresher.Run()
}

func (s *InMemoryStorage) Close() error {
	if s.cfg.IsEnabled() && s.cfg.Data().Dump.IsEnabled {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute*3)
		defer cancel()

		if err := s.dumper.Dump(ctx); err != nil {
			log.Err(err).Msg("[dump] failed to store cache dump")
		}
	}
	return nil
}

func (s *InMemoryStorage) Clear() {
	s.shardedMap.WalkShards(s.ctx, func(key uint64, shard *sharded.Shard[*model.Entry]) {
		shard.Clear()
	})
}

// Rand returns a random item from storage.
func (s *InMemoryStorage) Rand() (entry *model.Entry, ok bool) {
	return s.shardedMap.Rnd()
}

// Get retrieves a response by request and bumps its InMemoryStorage position.
// Returns: (response, releaser, found).
func (s *InMemoryStorage) Get(req *model.Entry) (ptr *model.Entry, found bool) {
	ptr, found = s.shardedMap.Get(req.MapKey())
	if !found || !ptr.IsSameFingerprint(req.Fingerprint()) {
		return nil, false
	} else {
		s.touch(ptr)
		return ptr, true
	}
}

// Set inserts or updates a response in the cache, updating Weight usage and InMemoryStorage position.
// On 'wasPersisted=true' must be called Entry.Finalize, otherwise Entry.Finalize.
func (s *InMemoryStorage) Set(new *model.Entry) (persisted bool) {
	key := new.MapKey()

	// increase access counter of tinyLFU
	s.tinyLFU.Increment(key)

	// try to find existing entry
	if old, found := s.shardedMap.Get(key); found {
		if old.IsSameFingerprint(new.Fingerprint()) {
			// entry was found, no hash collisions, fingerprint check has passed, next check payload
			if old.IsSamePayload(new) {
				// nothing change, an existing entry has the same payload, just up the element in LRU list
				s.touch(old)
			} else {
				// payload has changes, updated it and up the element in LRU list of course
				s.update(old, new)
			}
			return true
		}
		// hash collision found, remove collision element and try to set new one
		s.Remove(old)
	}

	// check whether we are still into memory limit
	if s.ShouldEvict() { // if so then check admission by tinyLFU
		if victim, admit := s.balancer.FindVictim(new.ShardKey()); !admit || !s.tinyLFU.Admit(new, victim) {
			return false
		}
	}

	// insert a new one Entry into map
	s.shardedMap.Set(key, new)
	// insert a new one Entry LRU element into LRU list
	s.balancer.Push(new)

	return true
}

func (s *InMemoryStorage) Remove(entry *model.Entry) (freedBytes int64, hit bool) {
	s.balancer.Remove(entry.ShardKey(), entry.LruListElement())
	return s.shardedMap.Remove(entry.MapKey())
}

func (s *InMemoryStorage) Len() int64 {
	return s.shardedMap.Len()
}

func (s *InMemoryStorage) RealLen() int64 {
	return s.shardedMap.RealLen()
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

func (s *InMemoryStorage) WalkShards(ctx context.Context, fn func(key uint64, shard *sharded.Shard[*model.Entry])) {
	s.shardedMap.WalkShards(ctx, fn)
}

// touch bumps the InMemoryStorage position of an existing entry (MoveToFront) and increases its refCount.
func (s *InMemoryStorage) touch(existing *model.Entry) {
	s.balancer.Update(existing)
}

// update refreshes Weight accounting and InMemoryStorage position for an updated entry.
func (s *InMemoryStorage) update(existing, new *model.Entry) {
	existing.SwapPayloads(new)
	s.balancer.Update(existing)
}

func (s *InMemoryStorage) Dump(ctx context.Context) error { return s.dumper.Dump(ctx) }
func (s *InMemoryStorage) Load(ctx context.Context) error { return s.dumper.Load(ctx) }
func (s *InMemoryStorage) LoadVersion(ctx context.Context, v string) error {
	return s.dumper.LoadVersion(ctx, v)
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
					limit      = utils.FmtMem(int64(s.cfg.Storage().Size))
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
						Str("memLimit", strconv.Itoa(int(s.cfg.Storage().Size))).
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
