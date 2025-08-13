package upstream

import (
	"context"
	"fmt"
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
		case <-eachMinute:
			go c.watchMinuteErrRateAndReset()
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

			slot.Lock()
			if probes, shouldQuarantine := slot.shouldQuarantineByProbes(); shouldQuarantine {
				slot.quarantine(fmt.Sprintf("%d failed probes in a row", probes), false)
			}
			slot.Unlock()
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

			slot.Lock()
			if probes, shouldCure := slot.shouldCureByProbes(); shouldCure {
				slot.cure(fmt.Sprintf("%d success probes in a row", probes), false)
			} else if fallTime, shouldKill := slot.shouldKill(); shouldKill {
				slot.kill(fmt.Sprintf("%s of fall time", fallTime.String()), false)
			}
			slot.Unlock()
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

			slot.Lock()
			if probes, shouldResurrect := slot.shouldResurrect(); shouldResurrect {
				slot.resurrect(fmt.Sprintf("%d success probes in a row", probes), false)
			} else if fallTime, shouldBury := slot.shouldBury(); shouldBury {
				c.bury(slot, fmt.Sprintf("%s of fall time", fallTime.String()), false)
			}
			slot.Unlock()
		}(slot)
	}
	c.mu.RUnlock()
}

func (c *BackendCluster) checkErrRate(reset bool, interval time.Duration) {
	c.mu.RLock()
	for _, slot := range c.all {
		if !slot.isIdle() && slot.hasHealthyState() {
			go func(slot *backendSlot) {
				slot.Lock()
				defer slot.Unlock()

				// print backends state before reset and possible moves
				c.showBackendState(slot, interval)

				if reset {
					defer func() {
						slot.hotPathCounters.total.Store(0)
						slot.hotPathCounters.errors.Store(0)
					}()
				}

				errRate := slot.errRate()
				if errRate >= errMaxRateThreshold {
					why := fmt.Sprintf("too high error rate=%.f%%", errRate*100)
					slot.quarantine(why, false)
					return
				}

				if errRate < errMaxRateThreshold && errRate >= errMinRateThreshold {
					if throttles, shouldThrottle := slot.shouldThrottle(); shouldThrottle {
						why := fmt.Sprintf("high error rate=%.f%%", errRate*100)
						slot.throttle(why)
					} else {
						why := fmt.Sprintf("max throttles percent %d%% was reached", throttles*10)
						slot.quarantine(why, false)
					}
					return
				}

				if errRate < errMinRateThreshold {
					if throttles, shouldUnthrottle := slot.shouldUnthrottle(); shouldUnthrottle {
						why := fmt.Sprintf("low error rate, backend throttled for %d%% at now", throttles*10)
						slot.unthrottle(why)
					}
					return
				}
			}(slot)
		} else if reset {
			slot.hotPathCounters.total.Store(0)
			slot.hotPathCounters.errors.Store(0)
		}
	}
	// shot total backends stat under single mutex lock
	c.showHealthinessState()
	c.mu.RUnlock()
}

func (c *BackendCluster) watchMinuteErrRateAndReset() {
	const (
		itersBy5Sec = 12
		interval    = time.Second * 5
	)

	i := itersBy5Sec
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(interval):
			i = i - 1
			elapsed := time.Duration(itersBy5Sec-i) * interval
			c.checkErrRate(i <= 0, elapsed)
			if i <= 0 {
				return
			}
		}
	}
}

func (c *BackendCluster) showBackendState(slot *backendSlot, interval time.Duration) {
	var (
		total  = slot.hotPathCounters.total.Load()
		errors = slot.hotPathCounters.errors.Load()
	)

	log.Info().Msgf(
		"[upstream][%s] %s={probes: {succes=%d, error=%d}, rate: {err=%.f%%, total=%d, errors=%d, limit=%dreqs/s, RPS=%.f}}",
		slot.state.String(), slot.upstream.backend.id, slot.counters.sucProbes, slot.counters.errProbes,
		safeDivide(float64(errors), float64(total))*100, total, errors, slot.jitter.limit, float64(total)/interval.Seconds(),
	)
}

func (c *BackendCluster) showHealthinessState() {
	hb := healthyBackends.Load()
	if hb > 0 {
		var postfix string
		if hb > 1 {
			postfix = "s"
		}
		log.Info().Msgf("[upstream][cluster] has %d healthy backend%s (total=%d)", hb, postfix, len(c.all))
	} else {
		log.Info().Msgf("[upstream][cluster] hasn't healthy backends (total=%d)", len(c.all))
	}
}

func safeDivide(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
