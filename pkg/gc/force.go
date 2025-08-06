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
func Run(ctx context.Context, cfg config.Config) {
	if !cfg.ForceGC.Enabled {
		return
	}

	go func() {
		// Force GC walk-through every cfg.AtomicCache.forceGC.GCInterval
		gcTicker := time.NewTicker(cfg.ForceGC.Interval)
		defer gcTicker.Stop()

		log.Info().Msgf(
			"[force-GC] has been started with interval=%s",
			cfg.ForceGC.Interval,
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
