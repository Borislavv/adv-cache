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
	each5Sec := utils.NewTicker(ctx, time.Second*5)
	eachMinute := utils.NewTicker(ctx, time.Minute)

	const minuteItersBy5Secs = 12
	i := 0
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-each5Sec:
			go c.checkHealthyIdle()
			go c.checkQuarantine()
			go c.showBackendsState()
			if i >= minuteItersBy5Secs {
				go c.checkErrRate()
			} else {
				go c.checkErrRateAndResetErrRate()
			}
			i++
		case <-eachMinute:
			go c.checkDead()
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

func (c *BackendCluster) checkErrRateAndResetErrRate() {
	c.mu.RLock()
	for _, slot := range c.all {
		slot.total.Store(0)
		slot.errors.Store(0)
	}
	c.mu.RUnlock()
}

func (c *BackendCluster) showBackendsState() {
	c.mu.RLock()
	for _, slot := range c.all {
		var errRate float64
		total := slot.total.Load()
		if total > 0 {
			errRate = float64(slot.errors.Load()) / float64(total)
		}

		log.Info().Msgf("[upstream][cluster] backend '%s' is %s (sucProbs=%d, errProbs=%d, errRate=%.f%%{reqs=%d, errs=%d}, availRate=%d)",
			slot.backend.Name(), slotState(slot.state.Load()).String(), slot.sucProbes.Load(), slot.errProbes.Load(),
			errRate*100, slot.total.Load(), slot.errors.Load(), int64(slot.rate.Load().Limit()),
		)
	}
	c.mu.RUnlock()
}
