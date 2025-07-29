package lru

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/storage/lfu"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/rs/zerolog/log"
)

// InMemoryStorage is a Weight-aware, sharded InMemoryStorage cache with background eviction and refreshItem support.
type InMemoryStorage struct {
	ctx             context.Context                     // Main context for lifecycle control
	cfg             *config.Cache                       // CacheBox configuration
	shardedMap      *sharded.Map[*model.VersionPointer] // Sharded storage for cache entries
	tinyLFU         *lfu.TinyLFU                        // Helps hold more frequency used items in cache while eviction
	backend         repository.Backender                // Remote backend server.
	balancer        Balancer                            // Helps pick shards to evict from
	mem             int64                               // Current Weight usage (bytes)
	memoryThreshold int64                               // Threshold for triggering eviction (bytes)
}

// NewStorage constructs a new InMemoryStorage cache instance and launches eviction and refreshItem routines.
func NewStorage(ctx context.Context, cfg *config.Cache, backend repository.Backender) *InMemoryStorage {
	shardedMap := sharded.NewMap[*model.VersionPointer](ctx, cfg.Cache.Preallocate.PerShard)
	balancer := NewBalancer(ctx, shardedMap)

	db := (&InMemoryStorage{
		ctx:             ctx,
		cfg:             cfg,
		shardedMap:      shardedMap,
		balancer:        balancer,
		backend:         backend,
		tinyLFU:         lfu.NewTinyLFU(ctx),
		memoryThreshold: int64(float64(cfg.Cache.Storage.Size) * cfg.Cache.Eviction.Threshold),
	}).init().runLogger()

	NewRefresher(ctx, cfg, db).Run()
	NewEvictor(ctx, cfg, db, balancer).Run()

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

// Rand returns a random item from storage.
func (s *InMemoryStorage) Rand() (entry *model.VersionPointer, ok bool) {
	return s.shardedMap.Rnd()
}

var (
	GetFound                 = &atomic.Int64{}
	GetNotFound              = &atomic.Int64{}
	GetNotAcquired           = &atomic.Int64{}
	GetNotTheSameFingerprint = &atomic.Int64{}
	GetRequestsAcquired      = &atomic.Int64{}
)

// Get retrieves a response by request and bumps its InMemoryStorage position.
// Returns: (response, releaser, found).
func (s *InMemoryStorage) Get(req *model.Entry) (ptr *model.VersionPointer, found bool) {
	ptr, found = s.shardedMap.Get(req.MapKey())
	if !found {
		GetNotFound.Add(1)
		return nil, false
	}
	if !ptr.Acquire() {
		GetNotAcquired.Add(1)
		return nil, false
	}
	if !ptr.IsSameFingerprint(req.Fingerprint()) {
		ptr.Release()
		GetNotTheSameFingerprint.Add(1)
		return nil, false
	}
	if ptr.Entry != req { // different pointers
		req.Remove() // then remove request entry
		GetRequestsAcquired.Add(1)
	}
	s.touch(ptr)
	GetFound.Add(1)
	return ptr, true
}

var (
	SetTheSamePointer  = &atomic.Int64{}
	SetFoundAndUpdated = &atomic.Int64{}
	SetFoundAndTouched = &atomic.Int64{}
	SetInsertedNewOne  = &atomic.Int64{}
	SetNotAdmitted     = &atomic.Int64{}
)

// Set inserts or updates a response in the cache, updating Weight usage and InMemoryStorage position.
// On 'wasPersisted=true' must be called Entry.Finalize, otherwise Entry.Finalize.
func (s *InMemoryStorage) Set(new *model.VersionPointer) (entry *model.VersionPointer) {
	key := new.MapKey()

	// increase access counter of tinyLFU
	s.tinyLFU.Increment(key)

	// try to find existing entry
	if old, found := s.shardedMap.Get(key); found {
		if new.Entry == old.Entry {
			// just return because the new already acquired
			SetTheSamePointer.Add(1)
			return new
		}

		if old.Acquire() {
			if old.IsSameFingerprint(new.Fingerprint()) {
				// entry was found, no hash collisions, fingerprint check has passed, next check payload
				if old.IsSamePayload(new.Entry) {
					// nothing change, an existing entry has the same payload, just up the element in LRU list
					s.touch(old)
					SetFoundAndTouched.Add(1)
				} else {
					// payload has changes, updated it and up the element in LRU list of course
					s.update(old, new)
					SetFoundAndUpdated.Add(1)
				}

				new.Remove() // An attempt - because someone still possible has references to 'new' entry, we does now nothing about it.

				return old
			}

			// hash collision found, remove collision element and try to set new one
			s.Remove(old)
		}
	}

	// check whether we are still into memory limit
	if s.ShouldEvict() { // if so then check admission by tinyLFU
		if victim, admit := s.balancer.FindVictim(new.ShardKey()); !admit || !s.tinyLFU.Admit(new, victim) {
			SetNotAdmitted.Add(1)
			new.MarkAsDoomed()
			return new
		}
	}

	SetInsertedNewOne.Add(1)

	// insert a new one Entry into map
	s.shardedMap.Set(key, new)
	// insert a new one Entry LRU element into LRU list
	s.balancer.Push(new)

	return new
}

func (s *InMemoryStorage) Remove(entry *model.VersionPointer) (freedBytes int64, finalized bool) {
	s.shardedMap.Remove(entry.MapKey())
	return entry.Remove()
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
