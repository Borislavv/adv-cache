**High-Performance Golang HTTP Cache**

> **Read: \~15 ns/op | Write: \~45 ns/op | RPS: 150k**
> **Cache-as-a-Service**: Sharded in-memory HTTP cache middleware with TinyLFU-enhanced LRU eviction, background refresh, Prometheus metrics, and disk persistence.

---

## 🚀 Core Features

1. **Massively Sharded Map (2048 shards)**
   Distributes keys across 2048 independent shards to minimize lock contention and maximize concurrency.

2. **Per-Shard LRU Lists**
   Each shard maintains its own lightweight LRU list for precise, low-overhead eviction without scanning the entire cache.

3. **TinyLFU Access Filter**
   Integrates TinyLFU using background frequency sketches via a ring buffer. New entries are admitted only if they outperform existing ones, boosting hit rate and protecting hot keys.

4. **Selective Evictor**
   Eviction routine targets the top \~17% busiest shards (≈384 shards), removing the least-used items as identified by the TinyLFU filter for optimal freshness.

5. **Probabilistic Refresher**
   Background sampler picks random entries, applies `ShouldRefresh()` with a beta-based exponential timing algorithm, and updates through a configurable rate limiter to prevent stampedes.

6. **On-Demand GZIP Response**
   Responses larger than 1KB are compressed with GZIP and automatically tagged with `Content-Encoding: gzip` for efficient bandwidth usage.

7. **Disk Dump & Load**
   Snapshots entire cache to a gzipped file on disk and reloads on startup. Simple overwrite strategy—no rotation.

8. **Flexible Request Keying**
   Matches request path, filters query parameters and headers, and constructs cache keys based on configured rules for precise control.

9. **Comprehensive Config**
   Supports environment variables and YAML (see [config.prod.yaml](https://github.com/Borislavv/advanced-cache/blob/master/config/config.prod.yaml)) with these sections:

   * **preallocate**: shard count, per-shard capacity
   * **eviction**: policy, thresholds
   * **storage**: type, memory limits
   * **refresh**: TTL, rate, beta, error TTL, backend URL
   * **persistence**: dump directory, file name
   * **rules**: path-based key and value extraction filters

10. **Metrics & Observability**
    Exposes Prometheus metrics (`/metrics`) and readiness/liveness probes for Kubernetes. Detailed debug logs track rolling-window stats (5s,1m,5m,1h).

---

## 🔧 Example Configuration

### Environment Variables

```
APP_ENV="prod"
FASTHTTP_SERVER_NAME="star.fast"
FASTHTTP_SERVER_PORT=":8010"
FASTHTTP_SERVER_SHUTDOWN_TIMEOUT="5s"
FASTHTTP_SERVER_REQUEST_TIMEOUT="10s"
IS_PROMETHEUS_METRICS_ENABLED="true"
LIVENESS_PROBE_TIMEOUT="5s"
```

### YAML Configuration

```yaml
cache:
  enabled: true
  preallocate:
    num_shards: 2048
    per_shard: 8196
  eviction:
    policy: "lru"
    threshold: 0.9
  storage:
    type: "malloc"
    size: 21474836480
  refresh:
    ttl: "24h"
    rate: 1000
    error_ttl: "30m"
    beta: 0.4
    backend_url: "https://google.com"
  persistence:
    is_enabled: true
    dump_dir: "public/dump"
    dump_name: "cache.dump.gz"
  rules:
    - path: "/api/v1/user"
      cache_key:
        query: ['param1','param2','param3','param4']
        headers: ['Accept-Encoding','X-Custom-ID']
      cache_value:
        headers: ['Cache-Control','X-Custom-ID']
    - path: "/api/v1/data"
      cache_key:
        query: ['param1','param2','param3','param4','param5']
        headers: ['Accept-Encoding','X-Custom-ID']
      cache_value:
        headers: ['Cache-Control','X-Custom-ID']
```

---

## 🏗️ Architecture Overview

* **Sharded Storage**: 2048 shards, each with map + per-shard LRU list + TinyLFU sketch.
* **Eviction**: Select busiest \~17% shards, purge bottom frequency items.
* **Refresh**: Random sampling, beta-probability, rate-limited updates.
* **Pooling**: Aggressive sync.Pool & custom BatchPool for buffers, requests, and responses.
* **HTTP API**: FastHTTP endpoints for cache ops, health, and metrics.
* **Persistence**: Gzipped dump on shutdown, load on startup.

---

## 🛠️ Quick Start

```bash
git clone https://github.com/Borislavv/advanced-cache.git
cd advanced-cache
go build -o advcache

# Run with envs:
APP_ENV="prod" 
FASTHTTP_SERVER_NAME="star.fast" 
FASTHTTP_SERVER_PORT=":8010" 
FASTHTTP_SERVER_SHUTDOWN_TIMEOUT="5s" 
FASTHTTP_SERVER_REQUEST_TIMEOUT="10s" 
IS_PROMETHEUS_METRICS_ENABLED="true" 
LIVENESS_PROBE_TIMEOUT="5s" 

// change ./config/{conifg.prod|config.local|config.test}.yaml if you need

./advcache
```

---

### 👊 Advanced cache vs. Ristretto
<img width="1372" alt="image" src="https://github.com/user-attachments/assets/f75bcb71-47a5-46c0-8670-2bc1d8a5e970" />

** See test by path for get more details: /pkg/storage/{ristretto_test|storage_test}.go** 

## 👤 Author & Maintainer

* **Glazunov Borislav**
* **Email**: [glazunov2142@gmail.com](mailto:glazunov2142@gmail.com)
* **Telegram**: @BorislavGlazunov

---

**Keywords:** `golang cache` `traefik middleware` `kubernetes cache` `LRU TinyLFU` `high-performance` `in-memory cache` `prometheus metrics`
