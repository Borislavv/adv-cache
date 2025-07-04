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
  env: "prod"
  enabled: true

  lifetime:
    max_req_dur: "100ms"
    escape_max_req_dur: "X-Google-Bot"

  upstream:
    url: "http://localhost:8020"
    rate: 1000
    timeout: "10s"

  preallocate:
    num_shards: 2048
    per_shard: 8196

  eviction:
    threshold: 0.9

  storage:
    size: 32212254720

  refresh:
    ttl: "12h"
    error_ttl: "1h"
    rate: 1000
    scan_rate: 10000
    beta: 0.4

  persistence:
    dump:
      enabled: true
      format: "gzip"
      dump_dir: "public/dump"
      dump_name: "cache.dump.gz"
      rotate_policy: "ring"
      max_files: 7

  rules:
    - path: "/api/v2/pagedata"
      ttl: "24h"
      error_ttl: "1h"
      beta: 0.3
      cache_key:
        query: ['project[id]', 'domain', 'language', 'choice']
        headers: ['Accept-Encoding', 'X-Project-ID']
      cache_value:
        headers: ['X-Project-ID']

    - path: "/api/v1/pagecontent"
      ttl: "36h"
      error_ttl: "3h"
      beta: 0.3
      cache_key:
        query: ['project[id]', 'domain', 'language', 'choice']
        headers: ['Accept-Encoding', 'X-Project-ID']
      cache_value:
        headers: ['X-Project-ID']
```

---

## Real numbers

- Read: ~50 ns/op
- Write: ~60 ns/op (synchronous and guaranteed, unlike Ristretto’s async writes)
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
