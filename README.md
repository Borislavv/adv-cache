# Advanced Cache

A custom in-memory HTTP cache for Go, built for high-load APIs and designed to work as middleware for Caddy and Traefik.  
Under the hood — sharding with LRU and TinyLFU, Doorkeeper filter, GZIP dump persistence, background refresh, and YAML configuration. Everything is bundled together with minimal allocations and predictable performance in mind.

---

## What’s inside

- In-memory caching with flexible LRU + TinyLFU configuration.
- Sharding to handle hundreds of millions of keys without degrading under load.
- Zero-allocation for paths, queries, and keys with fast hashing.
- GZIP compression and reuse via memory pools.
- Ready-made middleware for Caddy and Traefik — plug it into a reverse proxy or load balancer.
- Metrics via VictoriaMetrics.
- YAML configuration with hot reload support.
- Ready for Kubernetes deployment — liveness/readiness probes included.

---

## Architecture

- `pkg/storage` — caching layers (`lru`, `lfu`), eviction logic, memory tracking.
- `pkg/caddy/middleware` — Caddy v2 integration.
- `pkg/traefik/middleware` — Traefik integration.
- `pkg/model` — zero-allocation structs for keys and responses.
- `pkg/list`, `pkg/buffer` — internal lists, ring buffer and sharded maps.
- `pkg/prometheus/metrics` — metrics exporter for VictoriaMetrics.
- `internal/cache` — orchestration and REST API for management.

---

## Installation & build

**Requirements**
- Go 1.20+
- Caddy or Traefik (for embedded mode)
- Docker and Make (optional)

```bash
git clone https://github.com/Borislavv/advanced-cache.git
cd advanced-cache
go build -o advanced-cache ./cmd
```

---

## Example usage (Caddy)

Add the module in your `Caddyfile`:
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

---

## Example usage (Traefik)

Copy the plugin from `pkg/traefik` and register it as middleware.

---

## Configuration

All settings are defined in a YAML file. Example:
```yaml
cache:
  env: "dev"
  enabled: true

  logs:
    level: "info" # Any zerolog.Level.
    stats: true   # Should the statistic like num evictions, refreshes, rps, memory usage and so on be written in /std/out?

  forceGC:
    interval: "10s"

  lifetime:
    max_req_dur: "100ms"                # If a request lifetime is longer than 100ms then request will be canceled by context.
    escape_max_req_dur: "X-Google-Bot"  # If the header exists the timeout above will be skipped.

  upstream:
    url: "https://google.com" # downstream reverse proxy host:port
    rate: 80                  # Rate limiting reqs to backend per second.
    timeout: "10s"            # Timeout for requests to backend.

  preallocate:
    num_shards: 2048  # Fixed constant (see `NumOfShards` in code). Controls the number of sharded maps.
    per_shard: 256    # Preallocated map size per shard. Without resizing, this supports 2048*8196=~16785408 keys in total.
    # Note: this is an upper-bound estimate and may vary depending on hash distribution quality.

  eviction:
    threshold: 0.9    # Trigger eviction when cache memory usage exceeds 90% of its configured limit.

  storage:
    size: 32212254720 # 32GB of maximum allowed memory for the in-memory cache (in bytes).

  refresh:
    ttl: "12h"
    error_ttl: "3h"
    rate: 80          # Rate limiting reqs to backend per second.
    scan_rate: 10000  # Rate limiting of num scans items per second.
    beta: 0.4         # Controls randomness in refresh timing to avoid thundering herd (from 0 to 1).

  persistence:
    dump:
      enabled: true
      format: "gzip"              # gzip or raw json
      dump_dir: "public/dump"     # dump dir.
      dump_name: "cache.dump.gz"  # dump name
      rotate_policy: "ring"       # fixed, ring
      max_files: 7

  rules:
    - path: "/api/v2/pagedata"
      gzip:
        enabled: false
        threshold: 1024
      ttl: "20m"
      error_ttl: "5m"
      beta: 0.3 # Controls randomness in refresh timing to avoid thundering herd.
      cache_key:
        query: ['user', 'available', 'language', 'nodes'] # Match query parameters by prefix.
        headers:
          - Accept-Encoding   
          - Accept-Language  
      cache_value:
        headers:
          - Content-Type     
          - Content-Encoding  
          - Cache-Control      
          - Vary                
          - Strict-Transport-Security
          - Content-Length
          - Cache-Control
          - X-Content-Digest
          - Age

    - path: "/api/v1/pagecontent"
      gzip:
        enabled: true
        threshold: 1024
      ttl: "36h"
      error_ttl: "3h"
      beta: 0.3 # Controls randomness in refresh timing to avoid thundering herd.
      cache_key:
        query: ['user', 'available', 'language', 'nodes', 'cnt'] # Match query parameters by prefix.
        headers: ['Accept-Encoding', 'X-Project-ID']             # Match headers by exact value.
      cache_value:
        headers: ['X-Project-ID']                                # Store only when headers match exactly.

```

---

## Real numbers

- Read: ~40 ns/op
- Write: ~50 ns/op (synchronous and guaranteed, unlike Ristretto’s async writes)
- FastHTTP RPS: ~150,000 RPS+

---

## Use cases

- API response caching with low latency.
- Edge caching for static or SSR content.
- Caching at the reverse proxy/load balancer level (Caddy / Traefik).
- Can also run as a standalone FastHTTP service.

---

## Metrics

Exported in VictoriaMetrics-compatible format:
- `advanced_cache_http_requests_total{path,method,status}`
- `advanced_cache_memory_usage_bytes`
- `advanced_cache_items_total`

More coming soon...

---

## License

MIT — see [LICENSE](./LICENSE)

---

## Contacts

Maintainer — [Borislav Glazunov](https://github.com/Borislavv).  
Telegram: @BorislavGlazunov  
For bugs or questions, open an issue or reach out via GitHub.
