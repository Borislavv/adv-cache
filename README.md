
# Advanced Cache

Кастомный in-memory HTTP кэш для Go, разработан для high-load API и использования как middleware для Caddy и Traefik.  
Под капотом — шардирование с LRU и TinyLFU, Doorkeeper, дампинг с GZIP, фоновый refresh и настройка через YAML. Всё собрано в одну связку и ориентировано на минимальные аллокации и предсказуемую производительность.

---

## Что внутри

- In-memory кэширование с гибкой конфигурацией LRU + TinyLFU.
- Шардирование и работа с сотнями миллионов ключей без деградации под нагрузкой.
- Zero-allocation для пути, query и ключей с быстрым хешированием.
- GZIP-компрессия и reuse через memory pool.
- Готовые middleware для Caddy и Traefik — можно встроить в reverse proxy или балансировщик.
- Метрики через VictoriaMetrics.
- YAML-конфигурация с hot reload.
- Готов к деплою в Kubernetes — есть liveness/readiness пробы.

---

## Архитектура

- `pkg/storage` — слои кэша (`lru`, `lfu`), eviction, учёт памяти.
- `pkg/caddy/middleware` — интеграция с Caddy v2.
- `pkg/traefik/middleware` — интеграция с Traefik.
- `pkg/model` — zero-allocation структуры для ключей и ответов.
- `pkg/list`, `pkg/buffer` — внутренние списки, ring buffer и шардированные карты.
- `pkg/prometheus/metrics` — экспорт метрик для VictoriaMetrics.
- `internal/cache` — orchestration и REST API для управления.

---

## Установка и сборка

**Требования**
- Go 1.20+
- Caddy или Traefik (если нужен встраиваемый режим)
- Docker и Make (опционально)

```bash
git clone https://github.com/Borislavv/advanced-cache.git
cd advanced-cache
go build -o advanced-cache ./cmd
```

---

## Пример использования (Caddy)

Подключаете модуль в `Caddyfile`:
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

## Пример использования (Traefik)

Копируете плагин из `pkg/traefik` и регистрируете как middleware.

---

## Конфигурация

Вся настройка — через YAML-файл. Пример:
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
    - path: "/api/v2/pagedata"
      ttl: "24h"
      error_ttl: "1h"
      beta: 0.3 # Controls randomness in refresh timing to avoid thundering herd.
      cache_key:
        query: ['project[id]', 'domain', 'language', 'choice'] # Match query parameters by prefix.
        headers: ['Accept-Encoding', 'X-Project-ID']           # Match headers by exact value.
      cache_value:
        headers: ['X-Project-ID']                              # Store only when headers match exactly.

    - path: "/api/v1/pagecontent"
      ttl: "36h"
      error_ttl: "3h"
      beta: 0.3 # Controls randomness in refresh timing to avoid thundering herd.
      cache_key:
        query: ['project[id]', 'domain', 'language', 'choice'] # Match query parameters by prefix.
        headers: ['Accept-Encoding', 'X-Project-ID']           # Match headers by exact value.
      cache_value:
        headers: ['X-Project-ID']                              # Store only when headers match exactly.
```

---

## Реальные цифры

- Чтение: ~40 ns/op
- Запись: ~45 ns/op (синхронно и гарантированно, в отличие от асинхронной записи у Ristretto)
- RPS через FastHTTP: ~160 000 RPS

---

## Use cases

- API response кэширование с low-latency
- Edge caching для статики или SSR
- Кэширование на уровне reverse proxy или балансировщика (Caddy / Traefik)
- Использование как standalone FastHTTP сервис

---

## Метрики

Экспортируется в VictoriaMetrics-совместимом формате:
- `advanced_cache_http_requests_total{path,method,status}`
- `advanced_cache_memory_usage_bytes`
- `advanced_cache_items_total`
  
In progress, more coming soon...

---

## Лицензия

MIT — см. [LICENSE](./LICENSE)

---

## Контакты

Мейнтейнер — [Borislav Glazunov](https://github.com/Borislavv).  
Telegram: @BorislavGlazunov
Для багов и вопросов — открывайте issue или пишите через GitHub.
