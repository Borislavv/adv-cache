# traefik-http-cache-plugin

> **A high-performance, sharded in-memory cache with LRU eviction, background refresh, live metrics, and blazing fast HTTP API.**

---

## Features

- **High-throughput sharded cache:**  
  4096-way sharded map for scalable, lock-minimized concurrency.
- **Pluggable eviction policies:**  
  LRU eviction out of the box; supports custom eviction algorithms.
- **Background cache refresh:**  
  Probabilistic, randomized revalidation with tunable beta factor to prevent cache stampedes.
- **Zero-copy & pooling:**  
  Aggressive use of memory pooling (sync.Pool, custom BatchPool) for minimal allocations and GC pressure.
- **Atomic, lock-free fast paths:**  
  Most operations (`Get`, `Set`, `Touch`, `Remove`) are contention-free for hot-path performance.
- **Extensive metrics:**  
  Prometheus metrics and built-in debug logging for real-time performance insight.
- **Kubernetes/Cloud ready:**  
  Integrated liveness probe, graceful shutdown, health endpoints.
- **Streaming HTTP API:**  
  Blazing-fast API (fasthttp) for serving and populating cache, ready for production load.

---

## Architecture

- **Cache storage:**
  - Sharded map (`Map[Response]`) with per-shard locking for fine-grained concurrency.
  - Each shard maintains its own LRU list for precise, low-overhead eviction.
  - All reference counting, memory tracking, and object pooling are done per shard for efficiency.

- **Eviction/Refresh:**
  - LRU by default; memory usage-based triggering with configurable thresholds.
  - Periodic background refresher samples cold (least recently used) items, refreshing them with a randomized algorithm (`beta` parameter) to avoid thundering herd effects.
  - Aggressive memory pooling and slice interning for headers, bodies, and requests.

- **API:**
  - `/api/v1/cache/pagedata` endpoint (fasthttp + router) for GET/PUT operations.
  - Returns cached data when available; on miss, fetches from external backend and updates cache transparently.
  - Consistent error handling, JSON responses for 400/503, and rich logging.

- **Configurable:**
  - All parameters (shard count, eviction algo, refresh intervals, memory thresholds, etc.) set via environment variables or config file.

---

## Configuration

All major settings are controlled via environment variables:

| Variable                        | Description                                                             | Example                |
|---------------------------------|-------------------------------------------------------------------------|------------------------|
| `APP_ENV`                       | Environment: `prod`, `dev` or `test`                                    | `prod`                 |
| `BACKEND_URL`                   | Upstream external backend URL                                           | `http://backend:8080/` |
| `REVALIDATE_BETA`               | Background refresh beta (0.1...0.9 recommended)                         | `0.5`                  |
| `REVALIDATE_INTERVAL`           | Background revalidation interval (stale TTL on error = 10% of interval) | `10h`                  |
| `INIT_STORAGE_LEN_PER_SHARD`    | Initial map size per shard                                              | `4096`                 |
| `EVICTION_ALGO`                 | Eviction strategy (e.g., `lru`)                                         | `lru`                  |
| `MEMORY_FILL_THRESHOLD`         | Memory usage threshold to trigger eviction                              | `0.7`                  |
| `MEMORY_LIMIT`                  | Hard memory limit in bytes                                              | `4294967296` (4GB)     |
| `LIVENESS_PROBE_FAILED_TIMEOUT` | Liveness probe fail timeout                                             | `5s`                   |

---

## Important constants

These constants define the scaling, memory, and performance profile of the service. **Change with caution!**

1. **`sharded.ShardCount`**  
   Number of cache map shards (array length).
  - **Current value:** `4096`
  - **Recommendation:** Bump to `8192` or `16384` if you expect huge keyspace; optimal is 2K–20K items per shard.

2. **`synced.PreallocateBatchSize`**  
   Each `sync.Pool` (via `synced.BatchPool`) preallocates this many items on creator func call.
  - **Current value:** `1024`
  - **Warning:** On startup, total preallocated objects = `PreallocationBatchSize * 10`. Don’t set too high unless you have a ton of RAM.

3. **`lru.evictItemsPerIter`**  
   How many items to evict from a shard in one eviction iteration.

4. **`lru.maxEvictIterations`**  
   Maximum number of eviction iterations in a single eviction pass.

5. **`lru.topPercentageShards`**  
   Percentage of the most loaded shards that will be checked for eviction.

6. **`wheel.numOfRefreshesPerSec`**  
   The number of cache refreshes allowed per second (refresh rate limiter).
  - **Current value:** `1000`

---

## Internals

- **Sharded map:** 4096 shards by default; each is a map with its own lock and memory accounting.
- **Reference counting:** Ensures zero-use items are cleaned up, avoids race conditions.
- **Aggressive pooling:** Pools for responses, data buffers, requests, gzip writers/readers, and hashers.
- **Concurrency:** All cache operations are safe for high concurrency, no global locks.
- **Background jobs:**
  - LRU-based evictors run in parallel, removing least-used entries if memory threshold is exceeded.
  - Refresher samples cold cache items and refreshes them with a randomized interval to avoid stampedes.

---

## Metrics & Observability

- **Prometheus metrics:** Exposed via middleware and metrics package for full real-time insight.
- **Detailed debug logging:** Multi-window stats (RPS, avg request time) with different rolling windows (5s, 1m, 5m, 1h).
- **Liveness endpoints:** For Kubernetes/Cloud readiness checks.

---

## Building & Running

Build and run as a standalone binary, Docker, or in Kubernetes. All configuration is handled via environment variables.

---

## Author & Mainteiner

- **Author: Glazunov Borislav**
- **Telegram: @BorislavGlazunov**
- **Email:** glazunov2142@gmail.com


---

