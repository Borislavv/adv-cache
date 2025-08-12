package upstream

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/rs/zerolog/log"
	"go.uber.org/ratelimit"

	"sync"
	"sync/atomic"
	"time"
)

var healthyBackends = atomic.Int64{}

const maxThrottles = 9
const probesInRow = 5

type slotState int32

const (
	healthy slotState = iota
	sick
	dead
)

func (s slotState) String() string {
	switch s {
	case healthy:
		return "healthy"
	case sick:
		return "sick"
	case dead:
		return "dead"
	}
	return "_undefined_"
}

type provider struct {
	originCtx      context.Context
	cancelProvider context.CancelFunc
}

type counters struct {
	originRate int   // want to have been able to back off to origin value
	sucProbes  int64 // num of success probes in a row
	errProbes  int64 // num of errorred probes in a row
	throttles  int64 // num of throttles
}

// hot path counters
type hpCounters struct {
	total  atomic.Int64 // num of requests
	errors atomic.Int64 // num of errors
}

type timestamp struct {
	sickedAt time.Time
	killedAt time.Time
}

type jitter struct {
	limit      int
	originRate int
}

type upstream struct {
	cfg     *config.Backend
	backend *BackendNode
}

type downstream struct {
	outRateCh chan<- *backendSlot
}

// backendSlot - cluster node (contains backends)
type backendSlot struct {
	sync.RWMutex
	state           slotState
	provider        *provider
	hotPathCounters *hpCounters
	counters        *counters
	jitter          *jitter
	timestamp       *timestamp
	upstream
	downstream
}

// newBackendSlot - makes new backends slot for cluster
func newBackendSlot(ctx context.Context, cfg *config.Backend, outRate chan<- *backendSlot) *backendSlot {
	slot := &backendSlot{
		counters:        &counters{},
		timestamp:       &timestamp{},
		provider:        &provider{originCtx: ctx},
		downstream:      downstream{outRateCh: outRate},
		hotPathCounters: &hpCounters{total: atomic.Int64{}, errors: atomic.Int64{}},
		jitter:          &jitter{originRate: cfg.Rate},
		upstream:        upstream{cfg: cfg, backend: NewBackend(cfg)},
	}

	go slot.renewRateProvider(cfg.Rate, "init/shutdown")

	return slot
}

// probe - checks whether backends is healthy
func (s *backendSlot) probe() error {
	if err := s.backend.IsHealthy(); err != nil {
		s.Lock()
		s.counters.sucProbes = 0
		s.counters.errProbes += 1
		s.Unlock()
		return err
	}
	s.Lock()
	s.counters.sucProbes += 1
	s.counters.errProbes = 0
	s.Unlock()
	return nil
}

func (s *backendSlot) shouldCureByProbes() (probes int64, should bool) {
	return s.counters.sucProbes, s.counters.sucProbes > probesInRow
}

func (s *backendSlot) shouldQuarantineByProbes() (probes int64, should bool) {
	return s.counters.errProbes, s.counters.errProbes > probesInRow
}

func (s *backendSlot) shouldKill() (fallTime time.Duration, should bool) {
	sickedAt := s.timestamp.sickedAt
	if sickedAt.IsZero() {
		return 0, false
	}

	sucProbes := s.counters.sucProbes
	errProbes := s.counters.errProbes

	hasNotPositiveProbes := sucProbes == 0
	wasProbesThresholdOvercome := errProbes > probesInRow
	fallTime = time.Since(sickedAt)

	return fallTime, wasProbesThresholdOvercome && hasNotPositiveProbes && fallTime > downtimeForKill
}

func (s *backendSlot) shouldBury() (fallTime time.Duration, should bool) {
	killedAt := s.timestamp.killedAt
	if killedAt.IsZero() {
		return 0, false
	}

	sucProbes := s.counters.sucProbes
	errProbes := s.counters.errProbes

	hasNotPositiveProbes := sucProbes == 0
	wasProbesThresholdOvercome := errProbes > probesInRow
	fallTime = time.Since(killedAt)

	return fallTime, wasProbesThresholdOvercome && hasNotPositiveProbes && fallTime > downtimeForBury
}

func (s *backendSlot) shouldResurrect() (probes int64, should bool) {
	return s.counters.sucProbes, s.counters.sucProbes > probesInRow
}

func (s *backendSlot) isIdle() bool {
	return s.hotPathCounters.total.Load() == 0
}

func (s *backendSlot) hasHealthyState() bool {
	s.RLock()
	defer s.RUnlock()
	return s.state == healthy
}

func (s *backendSlot) hasSickState() bool {
	s.RLock()
	defer s.RUnlock()
	return s.state == sick
}

func (s *backendSlot) hasDeadState() bool {
	s.RLock()
	defer s.RUnlock()
	return s.state == dead
}

func (s *backendSlot) isThrottled() bool {
	s.RLock()
	defer s.RUnlock()
	return s.counters.throttles > 0
}

func (s *backendSlot) shouldThrottle() (throttles int64, should bool) {
	return s.counters.throttles, s.counters.throttles < maxThrottles && s.errRate() > errMinRateThreshold
}

func (s *backendSlot) shouldUnthrottle() (throttles int64, should bool) {
	return s.counters.throttles, s.hotPathCounters.total.Load() != 0 && s.counters.throttles > 0 && s.errRate() < errMinRateThreshold
}

// cure - moves slot from sick to healthy state
func (s *backendSlot) cure(why string, lock bool) bool {
	if lock {
		s.Lock()
		defer s.Unlock()
	}

	old := s.state
	if old != sick {
		return false
	}
	s.state = healthy

	log.Info().Msgf("[upstream] backend '%s' was cured (sick -> healthy) due to %s, throttling now...", s.backend.Name(), why)
	s.throttle("he was cured, smooth commissioning", maxThrottles-1)

	s.timestamp.sickedAt = time.Time{}
	s.timestamp.killedAt = time.Time{}

	return true
}

// quarantine - moves slot from healthy to sick state
func (s *backendSlot) quarantine(why string, lock bool) bool {
	if lock {
		s.Lock()
		defer s.Unlock()
	}

	old := s.state
	if old != healthy {
		return false
	}
	s.state = sick

	log.Info().Msgf("[upstream] backend '%s' was quarantined (healthy -> sick) due to %s, closing provider...", s.backend.Name(), why)
	s.closeRateProvider(false)

	s.timestamp.sickedAt = time.Now()
	s.timestamp.killedAt = time.Time{}

	return true
}

// kill - moves slot from sick to dead state
func (s *backendSlot) kill(why string, lock bool) bool {
	if lock {
		s.Lock()
		defer s.Unlock()
	}

	old := s.state
	if old != sick {
		return false
	}
	s.state = dead

	log.Info().Msgf("[upstream] backend '%s' was killed (sick -> dead) due to %s", s.backend.Name(), why)

	s.timestamp.killedAt = time.Now()

	return true
}

// resurrect - moves slot from dead to healthy state
func (s *backendSlot) resurrect(why string, lock bool) bool {
	if lock {
		s.Lock()
		defer s.Unlock()
	}

	old := s.state
	if old != dead {
		return false
	}
	s.state = sick

	s.counters.sucProbes = 0
	s.counters.errProbes = 0

	s.timestamp.sickedAt = time.Now()
	s.timestamp.killedAt = time.Time{}

	log.Info().Msgf("[upstream] backend '%s' was resurrected (dead -> sick) due to %s, quarantining now...", s.backend.Name(), why)

	return true
}

// throttle - reduces rate limit for one defaultThrottleStep or defaultThrottleStep*newThrottles if provided
func (s *backendSlot) throttle(why string, newThrottles ...int64) {
	var throttlesNum int64
	throttlesNum = s.counters.throttles + 1

	if len(newThrottles) > 1 || (len(newThrottles) == 1 && (newThrottles[0] > maxThrottles || newThrottles[0] < 0)) {
		panic("throttle: wrong usage of newThrottles param")
	} else if len(newThrottles) == 1 {
		throttlesNum = newThrottles[0]
	}

	or := float64(s.jitter.originRate)

	const hundred float64 = 100
	sp := or / hundred
	rt := int(or - (sp * float64(defaultThrottleStep*throttlesNum)))
	if rt < 0 {
		rt = 1
	}

	if throttlesNum <= maxThrottles {
		log.Info().Msgf("[upstream] throttling backend '%s' due to %s (newRate=%d)", s.backend.Name(), why, rt)

		s.counters.throttles = throttlesNum
		s.counters.sucProbes = 0
		s.counters.errProbes = 0
		s.hotPathCounters.total.Store(0)
		s.hotPathCounters.errors.Store(0)

		go s.renewRateProvider(rt, "throttling")
	}
}

// unthrottle - increases rate limit for one defaultThrottleStep
func (s *backendSlot) unthrottle(why string) {
	lm := s.jitter.limit
	or := float64(s.jitter.originRate)

	const hundred float64 = 100
	sp := or / hundred
	rt := float64(lm) + (sp * float64(defaultThrottleStep))
	if rt > or {
		rt = or
	}

	if s.counters.throttles > 0 {
		log.Info().Msgf("[upstream] unthrottling backend '%s' due to %s", s.backend.Name(), why)

		s.counters.throttles--
		s.counters.sucProbes = 0
		s.counters.errProbes = 0
		s.hotPathCounters.total.Store(0)
		s.hotPathCounters.errors.Store(0)

		go s.renewRateProvider(int(rt), "unthrottling")
	}
}

func (s *backendSlot) errRate() float64 {
	total := s.hotPathCounters.total.Load()
	errors := s.hotPathCounters.errors.Load()
	if total == 0 {
		return 0
	}
	return float64(errors) / float64(total)
}

// closeRateProvider - is private io.Closer interface implementation.
func (s *backendSlot) closeRateProvider(lock bool) {
	if lock {
		s.Lock()
		defer s.Unlock()
	}

	providerCloser := s.provider.cancelProvider
	providerCloser()

	s.counters.sucProbes = 0
	s.counters.errProbes = 0
	s.hotPathCounters.total.Store(0)
	s.hotPathCounters.errors.Store(0)
}

func (c *BackendCluster) bury(slot *backendSlot, reason string, lock bool) {
	c.mu.Lock()
	delete(c.all, slot.backend.ID())
	slot.closeRateProvider(lock)
	c.mu.Unlock()

	log.Info().Msgf("[upstream] backend '%s' was buried due to %s (not accessable any more)", slot.backend.Name(), reason)
}

// renewRateProvider - singleton worker (may exist only one instance)
// Creates a new one rateLimit provider and displaces previous by closing him rateLimit limiter.
func (s *backendSlot) renewRateProvider(limit int, reason string) {
	log.Info().Msgf("[upstream] backend '%s' rate limit %d, provider has been started due to %s", s.backend.Name(), limit, reason)
	defer log.Info().Msgf("[upstream] backend '%s' rateLimit %d, provider has been stopped due to %s", s.backend.Name(), limit, reason)

	s.Lock()
	defer s.Unlock()

	ctx, cancel := context.WithCancel(s.provider.originCtx)
	previousCancel := s.provider.cancelProvider
	s.provider.cancelProvider = cancel
	s.jitter.limit = limit

	go func() {
		healthyBackends.Add(1)
		defer healthyBackends.Add(-1)
		rate := ratelimit.New(limit)
		for {
			rate.Take()
			select {
			case <-ctx.Done():
				return
			case s.outRateCh <- s:
			}
		}
	}()

	if previousCancel != nil {
		previousCancel()
	}
}
