package upstream

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/utils"
	"github.com/rs/zerolog/log"
	"time"
)

const (
	defaultThrottleStep = 10
	errMinRateThreshold = 0.1
	errMaxRateThreshold = 0.7
)

var (
	downtimeForKill = time.Hour
	downtimeForBury = time.Hour * 24
)

// monitor - serves backends healthiness and availability (adds and takes load by rules)
func (c *BackendCluster) monitor(ctx context.Context) {
	each5sec := utils.NewTicker(ctx, time.Second*5)
	eachMinute := utils.NewTicker(ctx, time.Minute)

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-each5sec:
			go c.checkHealthyIdle()
			go c.checkQuarantine()
			go c.showBackendsState()
		case <-eachMinute:
			go c.checkDead()
			go c.watchMinuteErrRateAndReset()
		}
	}
}

func (c *BackendCluster) checkHealthyIdle() {
	c.mu.RLock()
	for _, slot := range c.all {
		if !slot.isIdle() || !slot.hasHealthyState() { // not idle, skip due to this slot will handle by checkErrRate() worker
			continue
		}
		go func(slot *backendSlot) {
			_ = slot.probe()
			if slot.shouldQuarantineByProbes() {
				slot.quarantine()
			}
		}(slot)
	}
	c.mu.RUnlock()
}

func (c *BackendCluster) checkQuarantine() {
	c.mu.RLock()
	for _, slot := range c.all {
		if !slot.hasSickState() {
			continue
		}
		go func(slot *backendSlot) {
			_ = slot.probe()
			if slot.shouldCureByProbes() {
				slot.cure()
			} else if slot.shouldKill() {
				slot.kill()
			}
		}(slot)
	}
	c.mu.RUnlock()
}

func (c *BackendCluster) checkDead() {
	c.mu.RLock()
	for _, slot := range c.all {
		if !slot.hasDeadState() {
			continue
		}

		go func(slot *backendSlot) {
			_ = slot.probe()
			if slot.shouldResurrect() {
				slot.resurrect()
			} else if slot.shouldBury() {
				c.bury(slot)
			}
		}(slot)
	}
	c.mu.RUnlock()
}

func (c *BackendCluster) checkErrRate() {
	c.mu.RLock()
	for _, slot := range c.all {
		if slot.isIdle() || !slot.hasHealthyState() {
			continue // checkHealthyIdle() worker will handle this slot
		}

		errRate := slot.errRate()
		if errRate >= errMaxRateThreshold {
			slot.quarantine()
		} else if errRate < errMaxRateThreshold && errRate >= errMinRateThreshold {
			if slot.mayBeThrottled() {
				slot.throttle()
			} else {
				slot.quarantine() // should be unthrottled after quarantine or killed otherwise
			}
		} else if slot.mayBeUnThrottled() && errRate < errMinRateThreshold {
			slot.unthrottle()
		}
	}
	c.mu.RUnlock()
}

func (c *BackendCluster) resetErrRate() {
	c.mu.RLock()
	for _, slot := range c.all {
		slot.total.Store(0)
		slot.errors.Store(0)
	}
	c.mu.RUnlock()
}

func (c *BackendCluster) watchMinuteErrRateAndReset() {
	i := 6
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(time.Second * 10):
			c.checkErrRate()
			if i = i - 1; i <= 0 {
				c.resetErrRate()
				return
			}
		}
	}
}

func (c *BackendCluster) showBackendsState() {
	c.mu.RLock()
	for _, slot := range c.all {
		var (
			bName     = slot.upstream.backend.Name()
			bState    = slotState(slot.state.Load()).String()
			total     = slot.total.Load()
			errors    = slot.errors.Load()
			errRate   float64
			sucProbes int64
			errProbes int64
			rateLimit int
		)

		slot.counters.RLock()
		sucProbes = slot.sucProbes
		errProbes = slot.errProbes
		slot.counters.RUnlock()

		slot.jitter.RLock()
		rateLimit = slot.jitter.Limit()
		slot.jitter.RUnlock()

		if total > 0 {
			errRate = float64(errors) / float64(total)
		}

		log.Info().Msgf("[upstream] backend '%s' is %s (sucProbs=%d, errProbs=%d, errRate=%.f%%{reqs=%d, errs=%d}, availRate=%d)",
			bName, bState, sucProbes, errProbes, errRate*100, total, errors, rateLimit,
		)
	}
	c.mu.RUnlock()
}
