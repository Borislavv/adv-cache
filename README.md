# Advanced Cache

> High-performance, zero-allocation, in-memory HTTP cache middleware for Caddy and Traefik.

Advanced Cache is a blazing-fast, production-ready caching layer for Go-based HTTP servers, designed with performance-first principles.
It features a sharded LRU/TinyLFU cache, zero-allocation request modeling, memory-aware eviction, GZIP compression, and deep integration with **Caddy** and **Traefik**.
If you like this project, ⭐️ it!

## ✨ Features

- 🔄 **In-Memory Caching** with configurable LRU + TinyLFU eviction algorithms
- 🔧 **Pluggable Storage Backends** with sharding support and atomic access
- 🧠 **Zero-allocation path + query modeling** with fast hashing and no garbage
- 🚀 **Ultra-low latency**, designed for high-throughput APIs and edge caches
- 📦 **GZIP compression**, response interning and memory pool reuse (`sync.Pool`)
- 🌐 **Caddy / Traefik Middleware** compatible with full setup via Caddyfile or Traefik YAML
- 📊 Built-in **metrics export via VictoriaMetrics** (no Prometheus client overhead)
- ☸️ **Kubernetes-ready** with native liveness/readiness probes
- ⚙️ **Hot reloadable** YAML configuration

## 🧱 Architecture Overview

The system is composed of several packages, with clear separation of concerns:

- `pkg/storage` — pluggable cache layers (`lru`, `lfu`), eviction policies, memory usage tracking
- `pkg/caddy/middleware` — Caddy v2 middleware integration, config parsing, runtime hook
- `pkg/traefik/middleware` — Traefik middleware plugin entrypoint
- `pkg/model` — Zero-allocation representations of requests, responses, and internal keys
- `pkg/list`, `pkg/buffer` — internal linked-lists, ring buffers, sharded maps
- `pkg/prometheus/metrics` — VictoriaMetrics integration for request/status/time metrics
- `internal/cache` — RESTful control interface, YAML reloading, cache orchestration

## 🛠 Installation & Build

### Requirements
- Go 1.20+
- [Caddy](https://caddyserver.com/) (for Caddy middleware integration)
- `make`, `docker` (optional for containerized builds)

### Building the Server
```bash
git clone https://github.com/Borislavv/advanced-cache.git
cd advanced-cache
go build -o advanced-cache ./cmd
```

## 🚀 Quick Start (Caddy)

Set up Caddyfile and copy middleware module from pkg/caddy (register your module in caddy/cmd/caddy/main.go -> `_ "github.com/caddyserver/caddy/v2/modules/advancedcache"`).
`Caddyfile` example:
```caddy
:80 {
  route {
    advanced_cache {
      config_path /config/config.prod.yaml
    }
    reverse_proxy http://localhost:8080
  }
}
```

## 🚀 Quick Start (Traefik)

Copy middleware plugin from pkg/traefik and register it in Traefik as middleware.

## ⚙️ Configuration

Configuration is loaded from a YAML file. Example:
```yaml
cache:
   env: "prod"
   enabled: true

   lifetime:
      max_req_dur: "100ms" # If a request lifetime is longer than 100ms then request will be canceled by context.
      escape_max_req_dur: "X-Google-Bot" # If the header exists the timeout above will be skipped.

   upstream:
      url: "http://localhost:8020" # downstream reverse proxy host:port
      rate: 1000 # Rate limiting reqs to backend per second.
      timeout: "10s" # Timeout for requests to backend.

   preallocate:
      num_shards: 2048 # Fixed constant (see `NumOfShards` in code). Controls the number of sharded maps.
      per_shard: 8196  # Preallocated map size per shard. Without resizing, this supports 2048*8196=~16785408 keys in total.
      # Note: this is an upper-bound estimate and may vary depending on hash distribution quality.

   eviction:
      threshold: 0.9 # Trigger eviction when cache memory usage exceeds 90% of its configured limit.

   storage:
      size: 32212254720 # 30 GB of maximum allowed memory for the in-memory cache (in bytes).

   refresh:
      ttl: "12h"
      error_ttl: "1h"
      rate: 1000 # Rate limiting reqs to backend per second.
      scan_rate: 10000 # Rate limiting of num scans items per second.
      beta: 0.4 # Controls randomness in refresh timing to avoid thundering herd (from 0 to 1).

   persistence:
      dump:
         enabled: true
         format: "gzip" # gzip or raw json
         dump_dir: "public/dump"
         dump_name: "cache.dump.gz"
         rotate_policy: "ring" # fixed, ring
         max_files: 7

   rules:
      - path: "/api/v2/user"
        ttl: "24h"
        error_ttl: "1h"
        beta: 0.3 # Controls randomness in refresh timing to avoid thundering herd.
        cache_key:
           query: ['project[id]', 'domain', 'language', 'choice'] # Match query parameters by prefix.
           headers: ['Accept-Encoding', 'X-Project-ID']           # Match headers by exact value.
        cache_value:
           headers: ['X-Project-ID']                              # Store only when headers match exactly.

      - path: "/api/v1/data"
        ttl: "36h"
        error_ttl: "3h"
        beta: 0.3 # Controls randomness in refresh timing to avoid thundering herd.
        cache_key:
           query: ['project[id]', 'domain', 'language', 'choice'] # Match query parameters by prefix.
           headers: ['Accept-Encoding', 'X-Project-ID']           # Match headers by exact value.
        cache_value:
           headers: ['X-Project-ID']                              # Store only when headers match exactly.

```

### Environment Variables
- `CONFIG_PATH`: Override path to config YAML
- `LOG_LEVEL`: Logging verbosity (default: `info`)

## 🧪 Use Cases

- API response caching
- Static asset edge caching
- Server-side HTML/SSR caching
- Caddy or Traefik based high-RPS load balancing with in-memory acceleration

## 🔌 Extendability

- Plug your own storage backend via `pkg/storage`
- Customize request hash, serialization, TTL rules per route
- Add middlewares via `pkg/server/middleware`

## 📈 Metrics & Monitoring

- `advanced_cache_http_requests_total{path,method,status}`
- `advanced_cache_memory_usage_bytes`
- `advanced_cache_items_total`
- `/metrics` exposes data in VictoriaMetrics-compatible format

## 🪪 License
MIT — see [LICENSE](./LICENSE) for full text.

## 📬 Contact
Maintained by [Borislav Glazunov](https://github.com/Borislavv). For support, open an issue or reach out via GitHub.
