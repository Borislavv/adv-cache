package counter

import "sync/atomic"

var (
	Total    = &atomic.Int64{}
	Hits     = &atomic.Int64{}
	Misses   = &atomic.Int64{}
	Proxies  = &atomic.Int64{}
	Errors   = &atomic.Int64{}
	Panics   = &atomic.Int64{}
	Duration = &atomic.Int64{} // UnixNano
)
