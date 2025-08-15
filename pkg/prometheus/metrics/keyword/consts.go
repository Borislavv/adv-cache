package keyword

var (
	AvgDuration              = "adv_cache_avg_duration_ns"
	RPS                      = "adv_cache_rps"
	Total                    = "adv_cache_total"   // num of total requests
	Errored                  = "adv_cache_errors"  // num of errors
	Panicked                 = "adv_cache_panics"  // num of panics
	Proxied                  = "adv_cache_proxies" // num of proxy requests
	Hits                     = "adv_cache_cache_hits"
	Misses                   = "adv_cache_cache_misses"
	MapMemoryUsageMetricName = "adv_cache_cache_memory_usage"
	MapLength                = "adv_cache_cache_length"
)
