# Prometheus metrics

## Main cache

1. adv_cache_rps
2. adv_cache_total
3. adv_cache_errors
4. adv_cache_panics
5. adv_cache_proxies
6. adv_cache_cache_hits
7. adv_cache_cache_misses
8. adv_cache_cache_memory_usage
9. adv_cache_cache_length

## Upstream cluster

### State
1. adv_backend_is_healthy
2. adv_backend_is_sick
3. adv_backend_is_dead
4. adv_backend_is_buried

### Moves
1. adv_backend_quarantined_total
2. adv_backend_cured_total
3. adv_backend_killed_total
4. adv_backend_resurrected_total
5. adv_backend_buried_total

### Per backend
1. adv_backend_requests_total
2. adv_backend_errors_total
3. adv_backend_error_rate
4. adv_backend_probes_success_total
5. adv_backend_probes_failed_total
6. adv_backend_rate_limit_total
7. adv_backend_throttled_total
8. adv_backend_unthrottled_total
9. adv_backend_throttle_level
10. adv_backend_current_rps
