package middleware

import (
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/rs/zerolog/log"
	"strconv"
	"sync/atomic"
	"time"
)

// runControllerLogger runs a goroutine to periodically log RPS and avg duration per window, if debug enabled.
func (m *TraefikCacheMiddleware) runControllerLogger() {
	go func() {
		t := utils.NewTicker(m.ctx, time.Second*5)
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-t:
				m.logAndReset()
			}
		}
	}()
}

// logAndReset prints and resets stat counters for a given window (5s).
func (m *TraefikCacheMiddleware) logAndReset() {
	const secs int64 = 5

	var (
		avg string
		cnt = atomic.LoadInt64(&m.count)
		dur = time.Duration(atomic.LoadInt64(&m.duration))
		rps = strconv.Itoa(int(cnt / secs))
	)

	if cnt <= 0 {
		return
	}

	avg = (dur / time.Duration(cnt)).String()

	logEvent := log.Info()

	if m.cfg.IsProd() {
		logEvent.
			Str("target", "controller").
			Str("rps", rps).
			Str("served", strconv.Itoa(int(cnt))).
			Str("periodMs", "5000").
			Str("avgDuration", avg)
	}

	logEvent.Msgf("[controller][5s] served %d requests (rps: %s, avgDuration: %s)", cnt, rps, avg)

	atomic.StoreInt64(&m.count, 0)
	atomic.StoreInt64(&m.duration, 0)
}
