package middleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/router/counter"
	"github.com/Borislavv/advanced-cache/pkg/router/route"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/rs/zerolog/log"
	"strconv"
	"time"
)

type MetricsLogger struct {
	ctx     context.Context
	cfg     *config.Cache
	storage storage.Storage
	metrics metrics.Meter
}

func NewMetricsLogger(ctx context.Context, cfg *config.Cache, storage storage.Storage, metrics metrics.Meter) *MetricsLogger {
	return &MetricsLogger{
		ctx:     ctx,
		cfg:     cfg,
		storage: storage,
		metrics: metrics,
	}
}

func (l *MetricsLogger) Run() {
	go func() {
		metricsTicker := utils.NewTicker(l.ctx, time.Second)

		var (
			// 5s accumulators
			totalNum         int64
			hitsNum          int64
			missesNum        int64
			errorsNum        int64
			panicsNum        int64
			proxiedNum       int64
			totalDurationNum int64

			accHourly   counters
			acc12Hourly counters
			acc24Hourly counters

			// different interval tickers
			eachHour   = time.NewTicker(time.Hour)
			each12Hour = time.NewTicker(12 * time.Hour)
			each24Hour = time.NewTicker(24 * time.Hour)
		)

		const logIntervalSecs = 5
		i := logIntervalSecs
		prev := time.Now()

		for {
			select {
			case <-l.ctx.Done():
				return

			case <-metricsTicker:
				totalNumLoc := counter.Total.Swap(0)
				hitsNumLoc := counter.Hits.Swap(0)
				missesNumLoc := counter.Misses.Swap(0)
				proxiedNumLoc := counter.Proxies.Swap(0)
				errorsNumLoc := counter.Errors.Swap(0)
				panicsNumLoc := counter.Panics.Swap(0)
				totalDurationNumLoc := counter.Duration.Swap(0)

				// metrics export
				var avgDuration float64
				if totalNumLoc > 0 {
					avgDuration = float64(totalDurationNumLoc) / float64(totalNumLoc)
				}
				memUsage, length := l.storage.Stat()
				l.metrics.SetCacheLength(uint64(length))
				l.metrics.SetCacheMemory(uint64(memUsage))
				l.metrics.SetHits(uint64(hitsNumLoc))
				l.metrics.SetMisses(uint64(missesNumLoc))
				l.metrics.SetErrors(uint64(errorsNumLoc))
				l.metrics.SetPanics(uint64(panicsNumLoc))
				l.metrics.SetProxiedNum(uint64(proxiedNumLoc))
				l.metrics.SetRPS(float64(totalNumLoc))
				l.metrics.SetAvgResponseTime(avgDuration)

				totalNum += totalNumLoc
				hitsNum += hitsNumLoc
				missesNum += missesNumLoc
				errorsNum += errorsNumLoc
				panicsNum += panicsNumLoc
				proxiedNum += proxiedNumLoc
				totalDurationNum += totalDurationNumLoc

				accHourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, panicsNumLoc, proxiedNumLoc, totalDurationNumLoc)
				acc12Hourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, panicsNumLoc, proxiedNumLoc, totalDurationNumLoc)
				acc24Hourly.add(totalNumLoc, hitsNumLoc, missesNumLoc, errorsNumLoc, panicsNumLoc, proxiedNumLoc, totalDurationNumLoc)

				if i == logIntervalSecs {
					elapsed := time.Since(prev)
					duration := time.Duration(int(avgDuration))
					rps := float64(totalNum) / elapsed.Seconds()

					if duration == 0 && rps == 0 {
						continue
					}

					var target string
					if route.IsCacheEnabled() {
						target = "cache-controller"
					} else {
						target = "proxy-controller"
					}

					log.Info().
						Str("target", target).
						Float64("rps", rps).
						Int64("served", totalNum).
						Int64("hits", hitsNum).
						Int64("missed", missesNum).
						Int64("errors", errorsNum).
						Int64("panics", panicsNum).
						Str("periodMs", strconv.Itoa(logIntervalSecs*1000)).
						Str("avgDuration", duration.String()).
						Str("elapsed", elapsed.String()).
						Msgf("[%s][%s]", target, elapsed.String())

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
				l.logLong("1h", accHourly)
				accHourly.reset()

			case <-each12Hour.C:
				l.logLong("12h", acc12Hourly)
				acc12Hourly.reset()

			case <-each24Hour.C:
				l.logLong("24h", acc24Hourly)
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
	panics   int64
	proxied  int64
	duration int64
}

func (c *counters) add(total, hits, misses, errors, panics, proxied, dur int64) {
	c.total += total
	c.hits += hits
	c.misses += misses
	c.errors += errors
	c.panics += panics
	c.proxied += proxied
	c.duration += dur
}

func (c *counters) reset() {
	c.total, c.hits, c.misses, c.errors, c.panics, c.proxied, c.duration = 0, 0, 0, 0, 0, 0, 0
}

func (l *MetricsLogger) logLong(label string, c counters) {
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

	log.Info().
		Str("target", "cache-long-metrics").
		Str("period", label).
		Int64("total", c.total).
		Int64("hits", c.hits).
		Int64("misses", c.misses).
		Int64("errors", c.errors).
		Int64("panics", c.panics).
		Int64("proxied", c.proxied).
		Float64("avgRPS", avgRPS).
		Str("avgDuration", avgDur.String()).
		Msgf("[cache/proxy-controller][%s] ", label)
}
