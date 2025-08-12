package upstream

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/rate"
	"github.com/rs/zerolog/log"
	"github.com/savsgio/gotils/nocopy"
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

// backendSlot - cluster node (contains backends)
type backendSlot struct {
	nocopy.NoCopy
	ctx            context.Context
	cancelProvider atomic.Pointer[context.CancelFunc]
	originRate     int                          // want to have been able to back off to origin value
	state          atomic.Int32                 // current state
	total          atomic.Int64                 // num of requests
	errors         atomic.Int64                 // num of errors
	throttles      atomic.Int64                 // num of throttles
	sucProbes      atomic.Int64                 // num of success probes in a row
	errProbes      atomic.Int64                 // num of errorred probes in a row
	rate           atomic.Pointer[rate.Limiter] // atomic for hot switch of rate limiter on slot
	cfg            *config.Backend
	backend        *BackendNode
	outRate        chan<- *backendSlot // need to send current pointer on available rate slot (providing itself on execution requests)
	sickedAt       atomic.Int64        // unix nano
	killedAt       atomic.Int64        // unix nano
}

// newBackendSlot - makes new backends slot for cluster
func newBackendSlot(ctx context.Context, cfg *config.Backend, outRate chan<- *backendSlot) *backendSlot {
	slot := &backendSlot{
		ctx:            ctx,
		cancelProvider: atomic.Pointer[context.CancelFunc]{},
		cfg:            cfg,
		originRate:     cfg.Rate,
		rate:           atomic.Pointer[rate.Limiter]{},
		state:          atomic.Int32{},
		total:          atomic.Int64{},
		errors:         atomic.Int64{},
		throttles:      atomic.Int64{},
		errProbes:      atomic.Int64{},
		sucProbes:      atomic.Int64{},
		sickedAt:       atomic.Int64{},
		killedAt:       atomic.Int64{},
		outRate:        outRate,
	}

	ctx, cancel := context.WithCancel(ctx)
	slot.cancelProvider.Store(&cancel)
	slot.backend = NewBackend(cfg)

	go slot.renewRateProvider(cfg.Rate)

	return slot
}

// probe - checks whether backends is healthy
func (s *backendSlot) probe() error {
	if err := s.backend.IsHealthy(); err != nil {
		s.sucProbes.Store(0)
		s.errProbes.Add(1)
		return err
	}
	s.sucProbes.Add(1)
	s.errProbes.Store(0)
	return nil
}

func (s *backendSlot) shouldCureByProbes() bool {
	return s.sucProbes.Load() > probesInRow
}

func (s *backendSlot) shouldQuarantineByProbes() bool {
	return s.errProbes.Load() > probesInRow
}

func (s *backendSlot) shouldKill() bool {
	killedAt := s.killedAt.Load()
	if killedAt == 0 {
		return false
	}

	downtime := time.Duration(s.killedAt.Load() - time.Now().UnixNano())
	hasProbesThreshold := s.errProbes.Load() > probesInRow
	hasNotPositiveProbes := s.sucProbes.Load() == 0

	return hasProbesThreshold && hasNotPositiveProbes && downtime > downtimeForKill
}

func (s *backendSlot) shouldBury() bool {
	killedAt := s.killedAt.Load()
	if killedAt == 0 {
		return false
	}

	downtime := time.Duration(s.killedAt.Load() - time.Now().UnixNano())
	hasProbesThreshold := s.errProbes.Load() > probesInRow
	hasNotPositiveProbes := s.sucProbes.Load() == 0

	return hasProbesThreshold && hasNotPositiveProbes && downtime > downtimeForBury
}

func (s *backendSlot) shouldResurrect() bool {
	return s.sucProbes.Load() > probesInRow
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
	return s.throttles.Load() > 0
}

func (s *backendSlot) mayBeThrottled() bool {
	return s.throttles.Load() < maxThrottles && s.errRate() > errMinRateThreshold
}

func (s *backendSlot) mayBeUnThrottled() bool {
	return !s.isIdle() && s.throttles.Load() > 0 && s.errRate() < errMinRateThreshold
}

// cure - moves slot from sick to healthy state
func (s *backendSlot) cure() bool {
	old := slotState(s.state.Load())
	if old != sick {
		return false
	}

	s.throttle(maxThrottles - 1)
	s.total.Store(0)
	s.errors.Store(0)
	s.sickedAt.Store(0)
	s.killedAt.Store(0)
	s.sucProbes.Store(0)
	s.errProbes.Store(0)

	name := s.backend.Name()
	if s.state.CompareAndSwap(int32(sick), int32(healthy)) {
		log.Info().Msgf("[upstream][cluster] backend '%s' was cured (sick -> healthy)", name)
		return true
	} else {
		log.Info().Msgf("[upstream][cluster] backend '%s' was not cured because CAS failed", name)
		return false
	}
}

// quarantine - moves slot from healthy to sick state
func (s *backendSlot) quarantine() bool {
	old := slotState(s.state.Load())
	if old != healthy {
		return false
	}

	s.closeRateProvider()
	s.total.Store(0)
	s.errors.Store(0)
	s.sucProbes.Store(0)
	s.errProbes.Store(0)
	s.killedAt.Store(0)
	s.sickedAt.Store(time.Now().UnixNano())

	name := s.backend.Name()
	if s.state.CompareAndSwap(int32(healthy), int32(sick)) {
		s.sickedAt.Store(time.Now().UnixNano())
		log.Info().Msgf("[upstream][cluster] backend '%s' was quarantined (healthy -> sick)", name)
		return true
	} else {
		log.Info().Msgf("[upstream][cluster] backend '%s' was not quarantined because CAS failed", name)
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
	s.total.Store(0)
	s.errors.Store(0)
	s.sucProbes.Store(0)
	s.errProbes.Store(0)
	s.killedAt.Store(time.Now().UnixNano())

	name := s.backend.Name()
	if s.state.CompareAndSwap(int32(sick), int32(dead)) {
		s.killedAt.Store(time.Now().UnixNano())
		log.Info().Msgf("[upstream][cluster] backend '%s' was killed (sick -> dead)", name)
		return true
	} else {
		log.Info().Msgf("[upstream][cluster] backend '%s' was not killed because CAS failed", name)
		return false
	}
}

// resurrect - moves slot from dead to healthy state
func (s *backendSlot) resurrect() bool {
	old := slotState(s.state.Load())
	if old != dead {
		return false
	}

	s.throttle(maxThrottles - 1)
	s.total.Store(0)
	s.errors.Store(0)
	s.sickedAt.Store(0)
	s.killedAt.Store(0)
	s.sucProbes.Store(0)
	s.errProbes.Store(0)

	name := s.backend.Name()
	if s.state.CompareAndSwap(int32(dead), int32(sick)) {
		log.Info().Msgf("[upstream][cluster] backend '%s' was resurrected (dead -> healthy)", name)
		return true
	} else {
		log.Info().Msgf("[upstream][cluster] backend '%s' was not resurrected because CAS failed", name)
		return false
	}
}

// throttle - reduces rate of released tokens for given percent from origin config value.
func (s *backendSlot) throttle(newThrottles ...int64) {
	var repeatNum = s.throttles.Load() + 1
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
		rt = 0
	}

	if old := s.throttles.Load(); old < maxThrottles {
		if repeatNum > 1 {
			old = repeatNum
		} else {
			old += 1
		}
		s.throttles.Store(old) // must be applied anyway
		log.Info().Msgf("[upstream][cluster] throttling backend '%s', current rate %d", s.backend.Name(), rt)
		go s.renewRateProvider(rt)
	}
}

// throttle - reduces rate of released tokens for given percent from origin config value.
func (s *backendSlot) unthrottle() {
	const hundred float64 = 100
	or := float64(s.originRate)
	sp := or / hundred
	rt := float64(s.rate.Load().Limit()) + (sp * float64(defaultThrottleStep))
	if rt > or {
		rt = or
	}

	if old := s.throttles.Load(); old > 0 {
		if s.throttles.CompareAndSwap(old, old-1) {
			log.Info().Msgf("[upstream][cluster] unthrottling backend '%s' due to low error rate", s.backend.Name())
			go s.renewRateProvider(int(rt))
		} else {
			log.Info().Msgf("[upstream][cluster] unthrottling backend '%s' failed by CAS", s.backend.Name())
		}
	}
}

func (s *backendSlot) errRate() float64 {
	t := s.total.Load()
	e := s.errors.Load()
	if t == 0 {
		return 0
	}
	return float64(e) / float64(t)
}

// closeRateProvider - is private io.Closer interface implementation.
func (s *backendSlot) closeRateProvider() {
	(*s.cancelProvider.Load())()
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
// Creates a new one rate provider and displaces previous by closing him rate limiter.
func (s *backendSlot) renewRateProvider(rt int) {
	log.Info().Msgf("[upstream][cluster] backend '%s' rate %d, provider has been started", s.backend.Name(), rt)
	defer log.Info().Msgf("[upstream][cluster] backend '%s' rate %d, provider has been stopped", s.backend.Name(), rt)

	healthyBackendsNum.Add(1)
	defer healthyBackendsNum.Add(-1)

	ctx, cancel := context.WithCancel(s.ctx)

	l := rate.NewLimiter(ctx, rt, rt)
	s.rate.Store(l)

	if previousCancel := *s.cancelProvider.Swap(&cancel); previousCancel != nil {
		previousCancel() // close the previous rate provider
	}

	for {
		l.Take()
		select {
		case <-ctx.Done():
			cancel()
			return
		case s.outRate <- s:
		}
	}
}
