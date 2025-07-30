package middleware

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/rs/zerolog/log"
	"strconv"
	"time"
)

// runControllerLogger runs a goroutine to periodically log RPS and avg duration per window, if debug enabled.
func (m *TraefikCacheMiddleware) runLoggerMetricsWriter() {
	go func() {
		metricsTicker := utils.NewTicker(m.ctx, time.Second)

		var (
			// 5s логика
			totalNum         int64
			hitsNum          int64
			missesNum        int64
			errorsNum        int64
			proxiedNum       int64
			totalDurationNum int64

			accHourly   counters
			acc12Hourly counters
			acc24Hourly counters

			// тикеры
			eachHour   = time.NewTicker(time.Hour)
			each12Hour = time.NewTicker(12 * time.Hour)
			each24Hour = time.NewTicker(24 * time.Hour)
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
				proxiedNumLoc := proxies.Swap(0)
				errorsNumLoc := errors.Swap(0)
				totalDurationNumLoc := totalDuration.Swap(0)

				// metrics export
				var avgDuration float64
				if totalNumLoc > 0 {
					avgDuration = float64(totalDurationNumLoc) / float64(totalNumLoc)
				}
				memUsage, length := m.storage.Stat()
				m.metrics.SetCacheLength(uint64(length))
				m.metrics.SetCacheMemory(uint64(memUsage))
				m.metrics.SetHits(uint64(hitsNumLoc))
				m.metrics.SetMisses(uint64(missesNumLoc))
				m.metrics.SetErrors(uint64(errorsNumLoc))
				m.metrics.SetProxiedNum(uint64(proxiedNumLoc))
				m.metrics.SetRPS(float64(totalNumLoc))
				m.metrics.SetAvgResponseTime(avgDuration)

				totalNum += totalNumLoc
				hitsNum += hitsNumLoc
				missesNum += missesNumLoc
				errorsNum += errorsNumLoc
				proxiedNum += proxiedNumLoc
				totalDurationNum += totalDurationNumLoc

				accHourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, proxiedNumLoc, totalDurationNumLoc)
				acc12Hourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, proxiedNumLoc, totalDurationNumLoc)
				acc24Hourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, proxiedNumLoc, totalDurationNumLoc)

				if i == logIntervalSecs {
					elapsed := time.Since(prev)
					duration := time.Duration(int(avgDuration))
					rps := float64(totalNum) / elapsed.Seconds()

					if duration == 0 && rps == 0 {
						continue
					}

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
							"[%s][%s] served %d requests (rps: %.f, avg.dur.: %s hits: %d, misses: %d, errors: %d)",
							target, elapsed.String(), totalNum, rps, duration.String(), hitsNum, missesNum, errorsNum,
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

			case <-eachHour.C:
				logLong("1h", accHourly, m.cfg)
				accHourly.reset()

			case <-each12Hour.C:
				logLong("12h", acc12Hourly, m.cfg)
				acc12Hourly.reset()

			case <-each24Hour.C:
				logLong("24h", acc24Hourly, m.cfg)
				acc24Hourly.reset()
			}
		}
	}()
}

type counters struct {
	total    int64
	hits     int64
	misses   int64
	errors   int64
	proxied  int64
	duration int64
}

func (c *counters) add(total, hits, misses, errors, proxied, dur int64) {
	c.total += total
	c.hits += hits
	c.misses += misses
	c.errors += errors
	c.proxied += proxied
	c.duration += dur
}

func (c *counters) reset() {
	c.total, c.hits, c.misses, c.errors, c.proxied, c.duration = 0, 0, 0, 0, 0, 0
}

func logLong(label string, c counters, cfg *config.Cache) {
	if c.total == 0 {
		return
	}

	var (
		avgDur = time.Duration(0)
		avgRPS float64
	)

	if c.total > 0 {
		avgDur = time.Duration(int(c.duration / c.total))

		switch label {
		case "1h":
			avgRPS = float64(c.total) / 3600
		case "12h":
			avgRPS = float64(c.total) / (12 * 3600)
		case "24h":
			avgRPS = float64(c.total) / (24 * 3600)
		}
	}

	logEvent := log.Info()
	if cfg.IsProd() {
		logEvent = logEvent.
			Str("target", "cache-long-metrics").
			Str("period", label).
			Int64("total", c.total).
			Int64("hits", c.hits).
			Int64("misses", c.misses).
			Int64("errors", c.errors).
			Int64("proxied", c.proxied).
			Float64("avgRPS", avgRPS).
			Str("avgDuration", avgDur.String())
	}

	logEvent.Msgf("[cache][%s] total=%d hits=%d misses=%d errors=%d proxied=%d avgRPS=%.2f avgDur=%s",
		label, c.total, c.hits, c.misses, c.errors, c.proxied, avgRPS, avgDur.String())
}
