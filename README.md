# Advanced Cache (advCache)
High‑load in‑memory HTTP cache & reverse‑proxy for Go. Designed for low latency, **hundreds of thousands RPS**.

> Built around *sharded maps*, *LRU + TinyLFU admission*, *background refresh*, and a *passive/active upstream cluster* — with a hot path that avoids allocations and locks.

---

## ✨ Highlights
- **Sharded map (~2k shards)** with per‑shard LRU lists and a global shard balancer for proportional eviction.
- **Admission = TinyLFU + Doorkeeper (+ Count‑Min Sketch):** keep frequent keys, reject noise.
- **Write‑through / Read‑through** with **background revalidation** (TTL, β‑staggering, scan‑rate, rate‑limit).
- **Backend cluster** with passive error tracking, throttling, quarantine/cure, and fan‑in request pattern.
- **Compression pools** (gzip/brotli), zero‑copy header handling, and careful buffer reuse.
- **Per‑shard dump & restore** with CRC32 and version rotation (optional GZIP).
- **Fasthttp server** + small REST surface + Prometheus/VictoriaMetrics metrics.
- **K8s‑friendly** liveness, graceful shutdown, configurable GOMAXPROCS (automaxprocs on zero value).

---

## 🧭 Repository map (components)
> Overview — follow the links to dive deeper.

### 1) Storage
- `pkg/storage/lru/*`
    - **`storage.go`** – orchestration; wires LFU admission, refresher, dumper, evictor.
    - **`evictor.go`** – periodic proportional eviction: selects the heaviest shards (≈ top 17%) and evicts from their tail.
    - **`refresher.go`** – background revalidation engine (TTL, *beta* coefficient, scan & backend rate limits).
    - **`dumper.go`** – per‑shard dumps to `dump_dir/dump_name-<shard>-<ts>.dump[.gz]` with CRC32, **max_versions** rotation; load restores last complete snapshot.
    - **`balancer.go`** – tracks per‑shard memory and maintains an **ordered shard list** (most loaded first) for fast, fair eviction.
- `pkg/storage/lfu/*`
    - **`tiny_lfu.go`** – **TinyLFU** admission with two count‑min sketches (**curr, prev**) and a **doorkeeper** Bloom‑like filter; rotation periodically halves history.
- `pkg/storage/map/*`
    - **`map.go`** – **sharded map**. Constants show an intent for ~**2047 active shards** + **1 collision shard** (2048 total).
    - **`shard.go`** – per‑shard operations (Get/Set/Del), *Weight()* accounting, releaser pools.

### 2) HTTP layer
- `pkg/http/server/*` – thin Fasthttp server with router + middleware chain. Tuned buffer sizes, disabled normalizations, minimal parsing for speed.
- `internal/cache/api/*`
    - **`cache.go`** – main GET handler: cache lookup → upstream on miss/error → write‑through on 200.
    - **`on_off.go`** – feature switch: `/cache/on`, `/cache/off` (simple toggles, JSON).
    - **`clear.go`** – two‑step clear with short‑lived token (defense against accidental wipe).
- `pkg/http/responder` – writes from cached *Entry* or raw *fasthttp.Response* with zero‑copy headers; sets `Last-Revalidated`.
- `pkg/http/header` – small helpers (pooled RFC1123 formatting).

### 3) Modeling & pools
- `pkg/model/entry.go` – the **Entry**: key derivation (path + selected query + selected headers), request/response payload storage, weight accounting, CAS‑like lifecycle helpers.
- `pkg/pools/*` – slice pools for hot‑path temporary structures (e.g., key/value header views).

### 4) Upstream cluster
- `pkg/upstream/*`
    - **`backend.go`** – per‑backend HTTP client (tls, dialer, timeouts) with **rate limiting**; builds upstream URIs from request.
    - **`slot.go`** – backend slot state machine (**healthy → sick → dead**), hot‑path counters, throttling window and step, quarantine.
    - **`cluster.go`** – cluster manager: builds slots from `cfg.Cluster.Backends`, initial probes; used fan-in pattern of healthy backends rate limiters ("select" implements pseudo-random). Has two policies: `deny` and `await` to control the behavior of requests in case no backend is ready (all are busy).
    - **`monitor.go`** – workers: **minute error‑rate scan** (6‑s ticks), slow‑start, throttling/unthrottling, sick/dead idlers handling; emits cluster health snapshots.

### 5) Metrics & ops
- `pkg/prometheus/metrics/*` – VictoriaMetrics adapter + `/metrics` controller; counters for hits/misses/errors/panics, cache length, RPS, avg duration.
- `pkg/k8s/probe/liveness/*` – liveness service & HTTP endpoint integration.
- `pkg/gc` – periodic GC trigger (configurable).

### 6) Utilities
- `pkg/sort/key_value.go` – allocation‑free insertion sort for `[][2][]byte` (query/header canonicalization).
- `pkg/bytes/cmp.go` – short‑circuit eq + XXH3‑assisted equality for large slices.
- `pkg/list/*` – generic doubly‑linked list used by shard balancer.

### 7) Entrypoint & configuration
- `cmd/main.go` – boot sequence: config → upstreams → storage → HTTP → probes → metrics → GC → graceful shutdown.
- `pkg/config/config.go` – YAML config loader, hot reload friendly; exposes typed subtrees for cache, upstream, storage, eviction, refresh, metrics, k8s.

---

## ⚙️ Configuration (YAML)
See [`advCache.cfg.yaml`](./advCache.cfg.yaml) and `advCache.cfg.local.yaml` for a runnable setup.

```yaml
cache:
  env: "dev"                 # dev|prod|test
  enabled: true
  runtime:
    gomaxprocs: 12           # auto-tuned via automaxprocs at startup too
  api:
    name: "advCache"
    port: "8020"

  # Upstream cluster
  upstream:
    cluster:
      backends:
        - id: "b1"
          enabled: true
          scheme: "http"
          host: "127.0.0.1:8080"
          rate: 800          # per-backend limit (req/s)
          slow_start: 10s
          probe_interval: 2s
        - id: "b2"
          enabled: true
          scheme: "http"
          host: "127.0.0.1:8081"
          rate: 800          # per-backend limit (req/s)
          slow_start: 10s
          probe_interval: 2s
        - id: "b3"
          enabled: true
          scheme: "https"
          host: "example.backend.com"
          rate: 800          # per-backend limit (req/s)
          slow_start: 10s
          probe_interval: 2s

  # Cache I/O
  data:
    enabled: true
    dump:
      enabled: true
      dump_dir: "public/dump"
      dump_name: "cache.dump"
      crc32_control_sum: true
      max_versions: 3
      gzip: false
    mock:                # for local stress
      enabled: false
      length: 1000000

  # Memory budget
  storage:
    size: 34359738368    # 32 GiB

  eviction:
    enabled: true
    threshold: 0.95      # start eviction at 95% of budget

  refresh:
    enabled: true
    ttl: "24h"
    rate: 8              # backend RPS cap for refreshes
    scan_rate: 10000     # entries/s to scan
    beta: 0.4            # stagger factor to avoid herd
    coefficient: 0.5     # start refresh at 50% of TTL

  forceGC:
    enabled: true
    interval: "10s"

  metrics:
    # exposed at /metrics (VictoriaMetrics compatible)
    enabled: true

  # Key canonicalization (example)
  keys:
    cache_key:
      query:
        - project[id]
        - domain
        - language
        - choice
        - timezone
      headers:
        - Accept-Encoding
    cache_value:
      headers:
        - Content-Type
        - Content-Encoding
        - Cache-Control
        - Vary
```

> **Notes**
> - Shards: 2048.
> - Dump files are per‑shard; **load** picks the latest complete timestamp batch across shards.

---

## ▶️ Quick start
```bash
# Build
go build -o advCache cmd/main.go

# Run with the default config
./advCache -c ./advancedCache.cfg.yaml

# Or run in Docker
docker build -t advcache .
docker run --rm -p 8020:8020 -v $PWD/public/dump:/app/public/dump advcache
```

**HTTP surface:**
- `GET /{any:*}` – main cached endpoint (path + selected query + headers form the key).
- `GET /cache/on` / `GET /cache/off` – toggle cache without restart.
- `GET /metrics` – Prometheus/VictoriaMetrics.
- `GET /cache/clear` – two‑step clear (request token, then confirm with the token).

---

## 🏗️ Architecture
```
         ┌──────────────┐
         │  fasthttp    │
         │   router     │
         └──────┬───────┘
                │  GET /{any}
        ┌───────▼──────────────┐
        │   Cache Controller   │───metrics/hits/miss/err
        └───┬─────────┬────────┘
            │hit      │miss
            │         │
   ┌────────▼───┐     │              ┌─────────────────────┐
   │  Storage   │     │  fetch       │  Upstream Cluster   │
   │ (LRU/TLFU) │◄────┘──────────────┤  slots + workers    │
   └──────┬─────┘                    └─────────┬───────────┘
          │ evict (heavy shards)               │ throttle/quarantine
          │ dump/load                          │ error‑rate scan
          ▼                                    ▼
      per‑shard lists                     backend clients
```

**Hot path facts**
- Lookups are per‑shard with short critical sections; payload copies are avoided.
- Keys are formed from selected query & headers; canonicalization uses in‑place sort to avoid allocs.
- Response write uses header copy **once** and direct body set.

---

## 📈 Benchmarks (current)
- Storage: **Read ≈ 40 ns/op**, **Write ≈ 50 ns/op** (benchmarks provided).
- Stress cache: **1,000,000** random elements × **4 KiB** each (**~4.5 GiB**) at **~200,000 RPS** on a 6‑CPU box.
- Stress proxy: **1,000,000** random elements × **4 KiB** each (**~4.5 GiB**) at **~100,000 RPS** on a 6‑CPU box (the same cache app. has been used as a backend in stress tests).

---

## ✅ Testing & benchmarking
- Unit tests: storage map, list, TinyLFU; storage integration.
- Benchmarks: TinyLFU; list; (extend with storage hot‑path ops).
- Suggested additions:
    - End‑to‑end read‑through/write‑through with mocked upstreams (race + bench).
    - Dump/load fuzz (corrupt/partial files; CRC failures; version rotation).
    - Cluster lifecycle under fault injection: spikes, 5xx bursts, timeouts, slow‑start, idle revival.
    - Allocation budget tests (`-run=^$ -bench=... -benchmem` + `GODEBUG=madvdontneed=1`).
    - Concurrency/race tests on `Entry` lifecycle (Acquire/Release/Remove), shard eviction under pressure.

---

## 🔧 Production tuning
- **GOMAXPROCS**: leave `automaxprocs` on; pin containers with CPU quotas.
- **Memory budget**: watch `cache.storage.size`, start eviction at 90–95%.
- **Logging**: keep hot path silent; keep structured logs in workers only.
- **Compression**: pool writers/readers; prefer pre‑compressed bodies from upstream when possible.
- **Probes**: add readiness probe (not just liveness) if you gate traffic behind priming loads.
- **Sysctls**: bump `somaxconn`, ephemeral ports, and file descriptors for high fan‑out.

---

## 🧪 Observability (VictoriaMetrics/Prometheus)
Key series (names from `pkg/prometheus/metrics/keyword`):
- `adv_cache_hits_total`, `adv_cache_misses_total`, `adv_cache_errors_total`, `adv_cache_panicked_total`
- `adv_cache_proxied_total` – upstream fallbacks
- `adv_cache_map_length` – items in cache
- `adv_cache_avg_duration_seconds` – rolling average of request duration
- Custom cluster gauges: healthy/sick/dead backends

---

## 📦 Build & deploy
- **Dockerfile** provided; consider enabling `GODEBUG=gctrace=1` in staging to size memory budgets.
- Run under a reverse proxy or as a sidecar; health probes ready.
- Minimal external deps: Fasthttp, VictoriaMetrics, XXH3, automaxprocs.

---

## 🤝 Contributing
PRs are welcome. Please include:
- Short description and rationale.
- Benchmarks (`-benchmem`) and allocations deltas for hot‑path changes.
- Race tests for concurrency‑sensitive changes.

---

## 📄 License
MIT — see [LICENSE](./LICENSE).

---

## Maintainer
**Borislav Glazunov** — Telegram: `@glbrslv`, Gmail: glazunov2142@gmail.com. Issues and discussions welcome.
