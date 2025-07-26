package advancedcache

import (
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/rs/zerolog/log"
	"strconv"
	"time"
)

// runControllerLogger runs a goroutine to periodically log RPS and avg duration per window, if debug enabled.
func (m *CacheMiddleware) runLoggerMetricsWriter() {
	go func() {
		metricsTicker := utils.NewTicker(m.ctx, time.Second)

		var (
			totalNum         uint64
			hitsNum          uint64
			missesNum        uint64
			errorsNum        uint64
			proxiedNum       uint64
			totalDurationNum int64
		)

		const logIntervalSecs = 5
		i := logIntervalSecs
		prev := time.Now()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-metricsTicker:
				totalNumLoc := total.Swap(0)
				hitsNumLoc := hits.Swap(0)
				missesNumLoc := misses.Swap(0)
				errorsNumLoc := errors.Swap(0)
				proxiedNumLoc := totalNumLoc - hitsNumLoc - missesNumLoc - errorsNumLoc
				totalDurationNumLoc := totalDuration.Swap(0)

				var avgDuration float64
				if totalNumLoc > 0 {
					avgDuration = float64(totalDurationNumLoc) / float64(totalNumLoc)
				}

				memUsage, length := m.storage.Stat()
				m.metrics.SetCacheLength(uint64(length))
				m.metrics.SetCacheMemory(uint64(memUsage))
				m.metrics.SetHits(hitsNumLoc)
				m.metrics.SetMisses(missesNumLoc)
				m.metrics.SetErrors(errorsNumLoc)
				m.metrics.SetProxiedNum(proxiedNumLoc)
				m.metrics.SetRPS(float64(totalNumLoc))
				m.metrics.SetAvgResponseTime(avgDuration)

				totalNum += totalNumLoc
				hitsNum += hitsNumLoc
				missesNum += missesNumLoc
				errorsNum += errorsNumLoc
				proxiedNum += proxiedNumLoc
				totalDurationNum += totalDurationNumLoc

				if i == logIntervalSecs {
					elapsed := time.Since(prev)
					duration := time.Duration(int(avgDuration))
					rps := float64(totalNum) / elapsed.Seconds()

					logEvent := log.Info()

					var target string
					if enabled.Load() {
						target = "cache-controller"
					} else {
						target = "proxy-controller"
					}

					if m.cfg.IsProd() {
						logEvent.
							Str("target", target).
							Str("rps", strconv.Itoa(int(rps))).
							Str("served", strconv.Itoa(int(totalNum))).
							Str("periodMs", strconv.Itoa(logIntervalSecs*1000)).
							Str("avgDuration", duration.String()).
							Str("elapsed", elapsed.String())
					}

					if enabled.Load() {
						logEvent.Msgf(
							"[%s][%s] served %d requests (rps: %.f, avg.dur.: %s hits: %d, misses: %d, proxied: %d, errors: %d)",
							target, elapsed.String(), totalNum, rps, duration.String(), hitsNum, missesNum, proxiedNum, errorsNum,
						)
					} else {
						logEvent.Msgf(
							"[%s][%s] served %d requests (rps: %.f, avg.dur.: %s total: %d, proxied: %d, errors: %d)",
							target, elapsed.String(), totalNum, rps, duration.String(), totalNum, proxiedNum, errorsNum,
						)
					}

					totalNum = 0
					hitsNum = 0
					missesNum = 0
					errorsNum = 0
					proxiedNum = 0
					totalDurationNum = 0
					prev = time.Now()
					i = 0
				}
				i++
			}
		}
	}()
}
