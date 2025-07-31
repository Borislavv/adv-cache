package keyword

var (
	/** Common */
	AvgDuration = "avg_duration_ns"
	RPS         = "rps"
	Errored     = "errors"  // num of errors
	Panicked    = "panics"  // num of panics
	Proxied     = "proxies" // num of proxy requests
	/* Cache specifically */
	Hits                     = "cache_hits"
	Misses                   = "cache_misses"
	MapMemoryUsageMetricName = "cache_memory_usage"
	MapLength                = "cache_length"
)
