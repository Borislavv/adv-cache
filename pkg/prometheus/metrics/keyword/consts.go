package keyword

var (
	/** Common */
	RPS      = "rps"
	Errored  = "errors"  // num of errors
	Panicked = "panics"  // num of panics
	Proxied  = "proxies" // num of proxy requests
	/* Cache specifically */
	Hits                     = "cache_hits"
	Misses                   = "cache_misses"
	MapMemoryUsageMetricName = "cache_memory_usage"
	MapLength                = "cache_length"
)

func MetricsCounters() []string {
	return []string{
		RPS,
		Errored,
		Panicked,
		Proxied,
		Hits,
		Misses,
		MapMemoryUsageMetricName,
		MapLength,
	}
}
