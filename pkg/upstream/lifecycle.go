package upstream

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

func StartHealthChecks(ctx context.Context, c *BackendCluster) {
	go tickSick(ctx, c)
	go tickDead(ctx, c)
	go tickHealthyIdle(ctx, c)
	go tickHealthyErrorMonitor(ctx, c)
}

func tickSick(ctx context.Context, c *BackendCluster) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			for id, slot := range c.sick {
				if slot.backend.IsHealthy() == nil {
					c.mu.RUnlock()
					if err := c.Promote(id); err != nil {
						log.Warn().Err(err).Str("id", id).Msg("[upstream-healthcheck] promote failed")
					}
					c.mu.RLock()
					continue
				}
				if time.Since(time.Unix(0, slot.sickedAt.Load())) > time.Hour {
					c.mu.RUnlock()
					if err := c.Kill(id); err != nil {
						log.Warn().Err(err).Str("id", id).Msg("[upstream-healthcheck] kill failed")
					}
					c.mu.RLock()
				}
			}
			c.mu.RUnlock()
		}
	}
}

func tickDead(ctx context.Context, c *BackendCluster) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			for id, slot := range c.dead {
				if slot.backend.IsHealthy() == nil {
					c.mu.RUnlock()
					if err := c.Resurrect(id); err != nil {
						log.Warn().Err(err).Str("id", id).Msg("[upstream-healthcheck] resurrect failed")
					}
					c.mu.RLock()
					continue
				}
				if time.Since(time.Unix(0, slot.deadAt.Load())) > 24*time.Hour {
					c.mu.RUnlock()
					if err := c.Bury(id); err != nil {
						log.Warn().Err(err).Str("id", id).Msg("[upstream-healthcheck] bury failed")
					}
					c.mu.RLock()
				}
			}
			c.mu.RUnlock()
		}
	}
}

func tickHealthyIdle(ctx context.Context, c *BackendCluster) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			slots := c.healthy.Load()
			for _, slot := range *slots {
				prevReq := slot.reqCount.Swap(0)
				if prevReq > 0 {
					slot.idleChecks.Store(0)
					continue // backend used, reset check
				}
				// not used recently — probe IsHealthy()
				if slot.backend.IsHealthy() == nil {
					slot.idleChecks.Store(0)
					continue
				}
				// increment failed idle probes
				fails := slot.idleChecks.Add(1)
				if fails >= 5 {
					if c.Quarantine(slot.backend.ID()) == nil {
						log.Warn().Str("id", slot.backend.ID()).Msg("[upstream-healthcheck] backend idle unhealthy — quarantined")
					}
				}
			}
		}
	}
}

func tickHealthyErrorMonitor(ctx context.Context, c *BackendCluster) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			slots := c.healthy.Load()
			for _, slot := range *slots {
				reqs := slot.reqCount.Swap(0)
				errs := slot.errCount.Swap(0)
				if reqs == 0 {
					continue // skip if no data
				}
				ratio := float64(errs) / float64(reqs)

				if ratio > 0.10 {
					if time.Since(time.Unix(0, slot.lastThrottle.Load())) < time.Minute {
						continue
					}
					if err := slot.rateLimiter.Decrease(); err != nil {
						log.Error().Str("id", slot.backend.ID()).Msg("[upstream-throttle] max throttles reached, backend should be quarantined")
						_ = c.Quarantine(slot.backend.ID())
						continue
					}
					slot.lastThrottle.Store(time.Now().UnixNano())
					log.Warn().Str("id", slot.backend.ID()).Float64("error_rate", ratio).Msg("[upstream-throttle] backend throttled")
				} else {
					slot.rateLimiter.Increase()
				}
			}
		}
	}
}
