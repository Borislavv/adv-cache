package upstream

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/rate"
	"github.com/rs/zerolog/log"
	"github.com/savsgio/gotils/nocopy"
	"sync"
	"sync/atomic"
	"time"
)

var healthyBackendsNum = atomic.Int64{}

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
	sync.Mutex
	cancelProvider context.CancelFunc
}

type counters struct {
	sync.RWMutex
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
	sync.RWMutex
	sickedAt int64 // unix nano
	killedAt int64 // unix nano
}

type jitter struct {
	sync.RWMutex
	*rate.Limiter
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
	nocopy.NoCopy
	ctx   context.Context
	state atomic.Int32 // int32(slotState)
	provider
	hpCounters
	counters
	jitter
	upstream
	downstream
	timestamp
}

// newBackendSlot - makes new backends slot for cluster
func newBackendSlot(ctx context.Context, cfg *config.Backend, outRate chan<- *backendSlot) *backendSlot {
	slot := &backendSlot{
		ctx:        ctx,
		provider:   provider{},
		counters:   counters{RWMutex: sync.RWMutex{}, originRate: cfg.Rate},
		hpCounters: hpCounters{total: atomic.Int64{}, errors: atomic.Int64{}},
		jitter:     jitter{Limiter: rate.NewLimiter(ctx, cfg.Rate, cfg.Rate)},
		upstream:   upstream{cfg: cfg, backend: NewBackend(cfg)},
		downstream: downstream{outRateCh: outRate},
		timestamp:  timestamp{RWMutex: sync.RWMutex{}},
	}

	go slot.renewRateProvider(cfg.Rate)

	return slot
}

// probe - checks whether backends is healthy
func (s *backendSlot) probe() error {
	if err := s.backend.IsHealthy(); err != nil {
		s.counters.Lock()
		s.counters.sucProbes = 0
		s.counters.errProbes += 1
		s.counters.Unlock()
		return err
	}
	s.counters.Lock()
	s.counters.sucProbes += 1
	s.counters.errProbes = 0
	s.counters.Unlock()
	return nil
}

func (s *backendSlot) shouldCureByProbes() bool {
	s.counters.RLock()
	defer s.counters.RUnlock()
	return s.counters.sucProbes > probesInRow
}

func (s *backendSlot) shouldQuarantineByProbes() bool {
	s.counters.RLock()
	defer s.counters.RUnlock()
	return s.counters.errProbes > probesInRow
}

func (s *backendSlot) shouldKill() bool {
	s.timestamp.RLock()
	killedAt := s.timestamp.killedAt
	s.timestamp.RUnlock()
	if killedAt == 0 {
		return false
	}

	s.counters.RLock()
	sucProbes := s.counters.sucProbes
	errProbes := s.counters.errProbes
	s.counters.RUnlock()

	hasNotPositiveProbes := sucProbes == 0
	wasProbesThresholdOvercome := errProbes > probesInRow
	downtime := time.Duration(killedAt - time.Now().UnixNano())

	return wasProbesThresholdOvercome && hasNotPositiveProbes && downtime > downtimeForKill
}

func (s *backendSlot) shouldBury() bool {
	s.timestamp.RLock()
	killedAt := s.timestamp.killedAt
	s.timestamp.RUnlock()
	if killedAt == 0 {
		return false
	}

	s.counters.RLock()
	sucProbes := s.counters.sucProbes
	errProbes := s.counters.errProbes
	s.counters.RUnlock()

	hasNotPositiveProbes := sucProbes == 0
	wasProbesThresholdOvercome := errProbes > probesInRow
	downtime := time.Duration(killedAt - time.Now().UnixNano())

	return wasProbesThresholdOvercome && hasNotPositiveProbes && downtime > downtimeForBury
}

func (s *backendSlot) shouldResurrect() bool {
	s.counters.RLock()
	defer s.counters.RUnlock()
	return s.counters.sucProbes > probesInRow
}

func (s *backendSlot) isIdle() bool {
	return s.total.Load() == 0
}

func (s *backendSlot) hasHealthyState() bool {
	return slotState(s.state.Load()) == healthy
}

func (s *backendSlot) hasSickState() bool {
	return slotState(s.state.Load()) == sick
}

func (s *backendSlot) hasDeadState() bool {
	return slotState(s.state.Load()) == dead
}

func (s *backendSlot) isThrottled() bool {
	s.counters.RLock()
	defer s.counters.RUnlock()
	return s.counters.throttles > 0
}

func (s *backendSlot) mayBeThrottled() bool {
	s.counters.RLock()
	defer s.counters.RUnlock()
	return s.counters.throttles < maxThrottles && s.errRate() > errMinRateThreshold
}

func (s *backendSlot) mayBeUnThrottled() bool {
	s.counters.RLock()
	defer s.counters.RUnlock()
	return s.total.Load() != 0 && s.counters.throttles > 0 && s.errRate() < errMinRateThreshold
}

// cure - moves slot from sick to healthy state
func (s *backendSlot) cure() bool {
	old := slotState(s.state.Load())
	if old != sick {
		return false
	}

	name := s.backend.Name()
	if s.state.CompareAndSwap(int32(sick), int32(healthy)) {
		s.throttle(maxThrottles - 1)

		s.total.Store(0)
		s.errors.Store(0)

		s.counters.Lock()
		s.counters.sucProbes = 0
		s.counters.errProbes = 0
		s.counters.Unlock()

		s.timestamp.Lock()
		s.timestamp.sickedAt = 0
		s.timestamp.killedAt = 0
		s.timestamp.Unlock()

		log.Info().Msgf("[upstream] backend '%s' was cured (sick -> healthy)", name)
		return true
	} else {
		log.Info().Msgf("[upstream] backend '%s' was not cured because CAS failed", name)
		return false
	}
}

// quarantine - moves slot from healthy to sick state
func (s *backendSlot) quarantine() bool {
	old := slotState(s.state.Load())
	if old != healthy {
		return false
	}

	name := s.backend.Name()
	if s.state.CompareAndSwap(int32(healthy), int32(sick)) {
		s.closeRateProvider()

		s.total.Store(0)
		s.errors.Store(0)

		s.counters.Lock()
		s.counters.sucProbes = 0
		s.counters.errProbes = 0
		s.counters.Unlock()

		s.timestamp.Lock()
		s.timestamp.killedAt = 0
		s.timestamp.Unlock()

		s.timestamp.Lock()
		s.timestamp.sickedAt = time.Now().UnixNano()
		s.timestamp.Unlock()
		log.Info().Msgf("[upstream] backend '%s' was quarantined (healthy -> sick)", name)
		return true
	} else {
		log.Info().Msgf("[upstream] backend '%s' was not quarantined because CAS failed", name)
		return false
	}
}

// kill - moves slot from sick to dead state
func (s *backendSlot) kill() bool {
	old := slotState(s.state.Load())
	if old != sick {
		return false
	}

	s.closeRateProvider()

	name := s.backend.Name()
	if s.state.CompareAndSwap(int32(sick), int32(dead)) {
		s.total.Store(0)
		s.errors.Store(0)

		s.counters.Lock()
		s.counters.sucProbes = 0
		s.counters.errProbes = 0
		s.counters.Unlock()

		s.timestamp.Lock()
		s.timestamp.killedAt = time.Now().UnixNano()
		s.timestamp.Unlock()

		log.Info().Msgf("[upstream] backend '%s' was killed (sick -> dead)", name)
		return true
	} else {
		log.Info().Msgf("[upstream] backend '%s' was not killed because CAS failed", name)
		return false
	}
}

// resurrect - moves slot from dead to healthy state
func (s *backendSlot) resurrect() bool {
	old := slotState(s.state.Load())
	if old != dead {
		return false
	}

	name := s.backend.Name()
	if s.state.CompareAndSwap(int32(dead), int32(sick)) {
		s.throttle(maxThrottles - 1)

		s.total.Store(0)
		s.errors.Store(0)

		s.counters.Lock()
		s.counters.sucProbes = 0
		s.counters.errProbes = 0
		s.counters.Unlock()

		s.timestamp.Lock()
		s.timestamp.sickedAt = 0
		s.timestamp.killedAt = 0
		s.timestamp.Unlock()

		log.Info().Msgf("[upstream] backend '%s' was resurrected (dead -> healthy)", name)
		return true
	} else {
		log.Info().Msgf("[upstream] backend '%s' was not resurrected because CAS failed", name)
		return false
	}
}

// throttle - reduces rateLimit of released tokens for given percent from origin config value.
func (s *backendSlot) throttle(newThrottles ...int64) {
	s.counters.Lock()
	defer s.counters.Unlock()

	var repeatNum = s.throttles + 1
	if len(newThrottles) > 1 || (len(newThrottles) == 1 && (newThrottles[0] > maxThrottles || newThrottles[0] < 0)) {
		panic("throttle: wrong usage of newThrottles param")
	} else if len(newThrottles) == 1 {
		repeatNum = newThrottles[0]
	}
	const hundred float64 = 100
	or := float64(s.originRate)
	sp := or / hundred
	rt := int(or - (sp * float64(defaultThrottleStep*repeatNum)))
	if rt < 0 {
		rt = 1
	}

	if old := s.throttles; old < maxThrottles {
		if repeatNum > 1 {
			old = repeatNum
		} else {
			old += 1
		}
		s.throttles = old
		log.Info().Msgf("[upstream] throttling backend '%s', current rateLimit %d", s.backend.Name(), rt)
		go s.renewRateProvider(rt)
	}
}

// throttle - reduces rateLimit of released tokens for given percent from origin config value.
func (s *backendSlot) unthrottle() {
	s.counters.Lock()
	defer s.counters.Unlock()

	s.jitter.RLock()
	lm := s.jitter.Limit()
	s.jitter.RUnlock()

	const hundred float64 = 100
	or := float64(s.originRate)
	sp := or / hundred
	rt := float64(lm) + (sp * float64(defaultThrottleStep))
	if rt > or {
		rt = or
	}

	if s.throttles > 0 {
		s.throttles--
		log.Info().Msgf("[upstream] unthrottling backend '%s' due to low error rateLimit", s.backend.Name())
		go s.renewRateProvider(int(rt))
	}
}

func (s *backendSlot) errRate() float64 {
	total := s.total.Load()
	errors := s.errors.Load()
	if total == 0 {
		return 0
	}
	return float64(errors) / float64(total)
}

// closeRateProvider - is private io.Closer interface implementation.
func (s *backendSlot) closeRateProvider() {
	s.provider.Lock()
	providerCloser := s.provider.cancelProvider
	s.provider.Unlock()
	providerCloser()
}

func (c *BackendCluster) bury(slot *backendSlot) {
	c.mu.Lock()
	delete(c.all, slot.backend.ID())
	slot.closeRateProvider()
	c.mu.Unlock()
}

//////////////////////////////////////////////////////////////////
//							Workers								//
//////////////////////////////////////////////////////////////////

// renewRateProvider - singleton worker (may exist only one instance)
// Creates a new one rateLimit provider and displaces previous by closing him rateLimit limiter.
func (s *backendSlot) renewRateProvider(rt int) {
	log.Info().Msgf("[upstream] backend '%s' rateLimit %d, provider has been started", s.backend.Name(), rt)
	defer log.Info().Msgf("[upstream] backend '%s' rateLimit %d, provider has been stopped", s.backend.Name(), rt)

	healthyBackendsNum.Add(1)
	defer healthyBackendsNum.Add(-1)

	ctx, cancel := context.WithCancel(s.ctx)

	newLimiter := rate.NewLimiter(ctx, rt, rt)
	s.jitter.Lock()
	oldLimiter := s.jitter.Limiter
	s.jitter.Limiter = newLimiter
	s.jitter.Unlock()
	if oldLimiter != nil {
		oldLimiter.Stop()
	}

	s.provider.Lock()
	previousCancel := s.cancelProvider
	s.cancelProvider = cancel
	s.provider.Unlock()
	if previousCancel != nil {
		previousCancel()
	}

	for {
		newLimiter.Take()
		select {
		case <-ctx.Done():
			return
		case s.outRateCh <- s:
		}
	}
}
