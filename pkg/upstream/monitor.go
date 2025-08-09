package upstream

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

func StartHealthChecks(ctx context.Context, c *BackendCluster) {
	go tickSick(ctx, c)
	go tickDead(ctx, c)
	go tickHealthyIdle(ctx, c)
	go tickThrottleMonitor(ctx, c)
}

func PrewarmBackend(ctx context.Context, b Backend) error {
	deadline := time.Now().Add(5 * time.Second)
	failures := 0
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := b.IsHealthy()
		if err == nil {
			return nil // passed
		}
		failures++
		if failures >= 3 {
			return err // fail early
		}
		time.Sleep(500 * time.Millisecond)
	}
	return context.DeadlineExceeded
}

func tickSick(ctx context.Context, c *BackendCluster) {
	log.Debug().Msg("[upstream-healthcheck-sick] has been started]")
	defer log.Debug().Msg("[upstream-healthcheck-sick] has been stopped]")

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			for id, slot := range c.sick {
				fmt.Println("check sick: ", id)

				go func(slot *backendSlot) {
					if err := slot.backend.IsHealthy(); err == nil {
						if err = c.Promote(id); err != nil {
							log.Warn().Err(err).Str("id", id).Msgf("[upstream-healthcheck-sick] backend '%s' promote failed", id)
						}
						return
					} else {
						log.Warn().Err(err).Str("id", id).Msgf("[upstream-healthcheck-sick] backend '%s' still sick", id)
					}
					if time.Since(time.Unix(0, slot.sickedAt.Load())) > time.Hour {
						if err := c.Kill(id); err != nil {
							log.Warn().Err(err).Str("id", id).Msg("[upstream-healthcheck-sick] kill failed")
						}
					}
				}(slot)
			}
			c.mu.RUnlock()
		}
	}
}

func tickDead(ctx context.Context, c *BackendCluster) {
	log.Debug().Msg("[upstream-healthcheck-dead] has been started]")
	defer log.Debug().Msg("[upstream-healthcheck-dead] has been stopped]")

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			for id, slot := range c.dead {
				go func(slot *backendSlot) {
					if slot.backend.IsHealthy() == nil {
						if err := c.Resurrect(id); err != nil {
							log.Warn().Err(err).Str("id", id).Msg("[upstream-healthcheck-dead] resurrect failed")
						}
						return
					}
					if time.Since(time.Unix(0, slot.deadAt.Load())) > 24*time.Hour {
						if err := c.Bury(id); err != nil {
							log.Warn().Err(err).Str("id", id).Msg("[upstream-healthcheck-dead] bury failed")
						}
					}
				}(slot)
			}
			c.mu.RUnlock()
		}
	}
}

func tickHealthyIdle(ctx context.Context, c *BackendCluster) {
	log.Debug().Msg("[upstream-healthcheck-idle] has been started]")
	defer log.Debug().Msg("[upstream-healthcheck-idle] has been stopped]")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			slots := c.healthy.Load()
			for _, slot := range *slots {
				if slot.reqCount.Load() > 0 {
					slot.idleHealthCheckFailsInARow.Store(0)
					continue // backend used, reset check
				}
				// not used recently — probe IsHealthy()
				if slot.backend.IsHealthy() == nil {
					slot.idleHealthCheckFailsInARow.Store(0)
					continue
				}
				// increment failed idle probes
				if fails := slot.idleHealthCheckFailsInARow.Add(1); fails >= 6 {
					id := slot.backend.ID()
					if c.Quarantine(id) == nil {
						log.Warn().Str("id", id).Msgf("[upstream-healthcheck-idle] too many coughs in a row of idle backend '%s', moved to quarantine", id)
					}
				}
			}
		}
	}
}

func tickThrottleMonitor(ctx context.Context, c *BackendCluster) {
	log.Debug().Msg("[upstream-throttle] has been started]")
	defer log.Debug().Msg("[upstream-throttle] has been stopped]")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, slot := range *c.healthy.Load() {
				id := slot.backend.ID()
				totl := slot.reqCount.Load() // total requests
				errs := slot.errCount.Load() // num errors
				slot.reqCount.Store(0)
				slot.errCount.Store(0)
				if totl < 10 {
					continue // skip if not enough data
				}

				errRate := float64(errs) / float64(totl)
				if errRate > 0.10 { // errors rate > 10% of total totl
					if slot.isThrottleAvailableBySpentTime() {
						if rt, rtp, err := slot.throttle(); err != nil {
							log.Error().Msgf("[upstream-throttle] %s, backend '%s' should be quarantined "+
								"(rate=%d[%d%%], errorsRate[%.2f%%]={total: %d, errors: %d})", err.Error(), id, rt, rtp, errRate, totl, errs)
							if err = c.Quarantine(id); err != nil {
								log.Error().Msgf("[upstream-throttle] %s: failed to move backend '%s' to quarantine after failed throttling "+
									"(rate=%d[%d%%], errorsRate[%.2f%%]={total: %d, errors: %d})", err.Error(), id, rt, rtp, errRate, totl, errs)
							} else {
								log.Error().Msgf("[upstream-throttle] backend '%s' has been moved to quarantine "+
									"(rate=%d[%d%%], errorsRate[%.2f%%]={total: %d, errors: %d})", id, rt, rtp, errRate, totl, errs)
							}
						} else {
							log.Error().Msgf("[upstream-throttle] backend '%s' has been throttled "+
								"(rate=%d[%d%%], errRate[%.2f%%]={total: %d, errors: %d})", id, rt, rtp, errRate, totl, errs)
						}
					}
				} else {
					if slot.isUnThrottleAvailableBySpentTime() {
						if rt, rtp, err := slot.unthrottle(); err == nil {
							log.Error().Msgf("[upstream-throttle] %s, backend '%s' has been unthrottled "+
								"(rate=%d[%d%%], errorsRate[%.2f%%]={total: %d, errors: %d})", err.Error(), id, rt, rtp, errRate, totl, errs)
						}
					}
				}
			}
		}
	}
}
