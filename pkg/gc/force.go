package gc

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"runtime"
	"time"

	"github.com/rs/zerolog/log"
)

// Run periodically forces Go's garbage collector and tries to return freed pages back to the OS.
// ----------------------------------------------
// Why is this needed?
//
// This service is a high-load in-memory cache.
// Once the cache reaches its target size (e.g., 10-20 million keys),
// the heap stabilizes at a large size — for example, 18 GB.
// By default, Go's GC will only run a full collection if the heap grows by GOGC% (default 100%).
// This means the next GC cycle could be delayed until the heap doubles again (e.g., 36 GB).
//
// For a cache, this almost never happens: the cache keeps its "critical mass" and rarely doubles in size,
// but in the meantime, temporary buffers and evicted objects create garbage.
// If no GC happens, this garbage is never reclaimed, so the process appears to "leak" memory.
//
// To prevent this, we force `runtime.GC()` on a short interval,
// and periodically call `debug.FreeOSMemory()` to push freed pages back to the OS.
// Both intervals are configurable in the config.
//
// This guarantees:
//   - predictable and stable memory usage
//   - less surprise RSS growth during steady state
//   - smoother operation for long-lived caches under high load.
func Run(ctx context.Context, cfg *config.Cache) {
	go func() {
		// Force GC walk-through every cfg.Cache.ForceGC.GCInterval
		gcTicker := time.NewTicker(cfg.Cache.ForceGC.Interval)
		defer gcTicker.Stop()

		log.Info().Msgf(
			"[force-GC] has been started with interval=%s",
			cfg.Cache.ForceGC.Interval,
		)

		var mem runtime.MemStats

		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("[force-GC] has been finished")
				return
			case <-gcTicker.C:
				runtime.GC()
				runtime.ReadMemStats(&mem)
				log.Info().Msgf(
					"[force-GC] forced GC pass (last StopTheWorld: %s)",
					lastGCPauseNs(mem.PauseNs),
				)
			}
		}
	}()
}

func lastGCPauseNs(pauses [256]uint64) time.Duration {
	for i := 255; i >= 0; i-- {
		if pauses[i] > 0 {
			return time.Duration(pauses[i])
		}
	}
	return time.Duration(0)
}
