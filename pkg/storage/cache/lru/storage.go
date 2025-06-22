package lru

import (
	"context"
	"runtime"
	"strconv"
	"time"

	"github.com/Borislavv/traefik-http-cache-plugin/pkg/config"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/repository"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/cache/lfu"
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/utils"
	"github.com/rs/zerolog/log"
)

const dumpDir = "public/dump"

var (
	evictionStatCh = make(chan evictionStat, runtime.GOMAXPROCS(0)*4)
)

// evictionStat carries statistics for each eviction batch.
type evictionStat struct {
	items    int   // Number of evicted items
	freedMem int64 // Total freed Weight
}

// Storage is a Weight-aware, sharded Storage cache with background eviction and refreshItem support.
type Storage struct {
	ctx             context.Context               // Main context for lifecycle control
	cfg             *config.Cache                 // CacheBox configuration
	shardedMap      *sharded.Map[*model.Response] // Sharded storage for cache entries
	tinyLFU         *lfu.TinyLFU                  // Helps hold more frequency used items in cache while eviction
	backend         repository.Backender          // Remote backend server.
	refresher       Refresher                     // Background refresher (see refresher.go)
	balancer        Balancer                      // Helps pick shards to evict from
	mem             int64                         // Current Weight usage (bytes)
	memoryThreshold int64                         // Threshold for triggering eviction (bytes)
}

// NewStorage constructs a new Storage cache instance and launches eviction and refreshItem routines.
func NewStorage(
	ctx context.Context,
	cfg *config.Cache,
	balancer Balancer,
	refresher Refresher,
	backend repository.Backender,
	tinyLFU *lfu.TinyLFU,
	shardedMap *sharded.Map[*model.Response],
) *Storage {
	storage := &Storage{
		ctx:             ctx,
		cfg:             cfg,
		shardedMap:      shardedMap,
		refresher:       refresher,
		balancer:        balancer,
		backend:         backend,
		tinyLFU:         tinyLFU,
		memoryThreshold: int64(float64(cfg.Cache.Storage.Size) * cfg.Cache.Eviction.Threshold),
	}

	// Register all existing shards with the balancer.
	storage.shardedMap.WalkShards(func(shardKey uint64, shard *sharded.Shard[*model.Response]) {
		storage.balancer.Register(shard)
	})

	// Load dump of it exists in public/dump dir.
	storage.loadDumpIfExists()

	// Launch background refresher and evictors.
	storage.refresher.RunRefresher()
	storage.runEvictor()
	storage.runLogger()

	return storage
}

// Get retrieves a response by request and bumps its Storage position.
// Returns: (response, releaser, found).
func (c *Storage) Get(req *model.Request) (*model.Response, bool) {
	resp, found := c.shardedMap.Get(req.MapKey(), req.ShardKey())
	if found {
		c.touch(resp)
		return resp, true
	}
	return nil, false
}

// Set inserts or updates a response in the cache, updating Weight usage and Storage position.
func (c *Storage) Set(new *model.Response) {
	key := new.Request().MapKey()
	shardKey := new.Request().ShardKey()

	// Track access frequency
	c.tinyLFU.Increment(key)

	existing, found := c.shardedMap.Get(key, shardKey)
	if found {
		c.update(existing)
		return
	}

	// Admission control: if memory is over threshold, evaluate before inserting
	if c.shouldEvict() {
		victim, ok := c.balancer.FindVictim(shardKey)
		if !ok {
			return
		}
		if victim != nil && !c.tinyLFU.Admit(new, victim) {
			// New item is less frequent than victim, skip insertion
			return
		}
	}

	// Proceed with insert
	c.set(new)
}

// Del does not guarantee that the item will be deleted at this time because other users may exist.
func (c *Storage) del(key uint64) (freed int64, isHit bool) {
	return c.shardedMap.Remove(key)
}

// touch bumps the Storage position of an existing entry (MoveToFront) and increases its refCount.
func (c *Storage) touch(existing *model.Response) {
	c.balancer.Update(existing)
}

// update refreshes Weight accounting and Storage position for an updated entry.
func (c *Storage) update(existing *model.Response) {
	c.balancer.Update(existing)
}

// set inserts a new response, updates Weight usage and registers in balancer.
func (c *Storage) set(new *model.Response) {
	c.shardedMap.Set(new)
	c.balancer.Set(new)
}

// runLogger emits detailed stats about evictions, Weight, and GC activity every 5 seconds if debugging is enabled.
func (c *Storage) runLogger() {
	go func() {
		var (
			evictsNumPer5Sec int
			evictsMemPer5Sec int64
			ticker           = utils.NewTicker(c.ctx, 5*time.Second)
		)

		for {
			select {
			case <-c.ctx.Done():
				return
			case stat := <-evictionStatCh:
				evictsNumPer5Sec += stat.items
				evictsMemPer5Sec += stat.freedMem
				runtime.Gosched()
			case <-ticker:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)

				var (
					mem        = utils.FmtMem(c.shardedMap.Mem())
					length     = strconv.Itoa(int(c.shardedMap.Len()))
					gc         = strconv.Itoa(int(m.NumGC))
					limit      = utils.FmtMem(int64(c.cfg.Cache.Storage.Size))
					goroutines = strconv.Itoa(runtime.NumGoroutine())
					alloc      = utils.FmtMem(int64(m.Alloc))
					freedMem   = utils.FmtMem(evictsMemPer5Sec)
				)

				log.
					Info().
					//Str("target", "lru").
					//Str("freedMem", freedMem).
					//Str("len", length).
					//Str("GC", gc).
					//Str("memLimit", limit).
					//Str("goroutines", goroutines).
					Msgf(
						"[lru][5s] evicted (items: %d, freedMem: %s), "+
							"storage (usage: %s, len: %s, limit: %s), sys (alloc: %s, goroutines: %s, GC: %s)",
						evictsNumPer5Sec, freedMem, mem, length, limit, alloc, goroutines, gc,
					)

				evictsNumPer5Sec = 0
				evictsMemPer5Sec = 0
				runtime.Gosched()
			}
		}
	}()
}

func (c *Storage) loadDumpIfExists() {
	if err := c.LoadFromDir(c.ctx, dumpDir); err != nil {
		log.Warn().Msg("failed to load dump: " + err.Error())
	}
}

func (c *Storage) Stop() {
	// spawn a new one with limit for k8s timeout before the service will be received SIGKILL
	dumpCtx, dumpCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer dumpCancel()

	if err := c.DumpToDir(dumpCtx, dumpDir); err != nil {
		log.Err(err).Msg("failed to dump cache")
	}
}
