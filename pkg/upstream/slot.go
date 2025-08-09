package upstream

import (
	"context"
	"errors"
	"github.com/Borislavv/advanced-cache/pkg/rate"
	"github.com/rs/zerolog/log"
	"sync/atomic"
	"time"
)

type backendState uint32

const (
	stateHealthy backendState = iota
	stateSick
	stateDead
	stateBuried
)

type backendSlot struct {
	backend Backend                      // backend itself
	rate    atomic.Pointer[rate.Limiter] // hot reloadable throttler

	reqCount                   atomic.Uint32 // total reqs
	errCount                   atomic.Uint32 // num errors
	idleHealthCheckFailsInARow atomic.Uint32 // num of failed healthcheck probes to backend in a row

	throttles      atomic.Int32 // num of throttles
	lastThrottle   atomic.Int64 // unix name timestamp of last throttling
	lastUnThrottle atomic.Int64 // unix name timestamp of last unthrottling

	state    atomic.Uint32 // backendState
	sickedAt atomic.Int64  // UnixNano
	deadAt   atomic.Int64  // UnixNano
}

func newBackendSlot(ctx context.Context, backend Backend, initState backendState) *backendSlot {
	slot := &backendSlot{
		backend:                    backend,
		reqCount:                   atomic.Uint32{},
		errCount:                   atomic.Uint32{},
		idleHealthCheckFailsInARow: atomic.Uint32{},
		state:                      atomic.Uint32{},
		throttles:                  atomic.Int32{},
		lastThrottle:               atomic.Int64{},
		lastUnThrottle:             atomic.Int64{},
		sickedAt:                   atomic.Int64{},
		deadAt:                     atomic.Int64{},
	}

	slot.state.Store(uint32(initState))

	rt := backend.Cfg().Rate / 2 // TODO handle corner cases with calculations
	bt := rt/2 + 1
	slot.rate.Store(rate.NewLimiter(ctx, rt, bt))

	slot.throttles.Store(5) // start from 50% of load
	slot.lastThrottle.Store(time.Now().UnixNano())
	slot.lastUnThrottle.Store(time.Now().UnixNano() - int64(time.Second*50)) // TODO remove hard code (i know that tick per minute)

	log.Info().Str("id", backend.ID()).Msgf("[upstream-init] new backend '%s' slot created (rate=%d, percentage=%.2f)", backend.ID(), rt, 50.00)

	return slot
}

func (s *backendSlot) getState() backendState {
	return backendState(s.state.Load())
}

func (s *backendSlot) stateCAS(from, to backendState) bool {
	return s.state.CompareAndSwap(uint32(from), uint32(to))
}

const maxThrottles = 8

var MaxThrottlesReached = errors.New("max throttles reached")

func (s *backendSlot) isThrottleAvailableBySpentTime() bool {
	return s.lastThrottle.Load()+int64(time.Minute) > time.Now().UnixNano()
}

func (s *backendSlot) throttle() (newRate, newRatePercents int, err error) {
	for {
		lim := s.rate.Load()
		old := s.throttles.Load()
		if s.throttles.Load() >= maxThrottles {
			return int(lim.Limit()), int((old - 10) * 10), MaxThrottlesReached
		}

		if s.throttles.CompareAndSwap(old, old+1) {
			or := s.backend.Cfg().Rate
			rt := or / 10 * ((int(old) + 1) - 10)
			bt := rt/2 + 1
			s.rate.Store(rate.NewLimiter(s.rate.Load().Ctx(), rt, bt))
			s.lastThrottle.Store(time.Now().Unix())
			return rt, (rt - 10) * 10, nil
		}
	}
}

const minUnthrottles = 0

var MinUnthrottlesReached = errors.New("min unthrottles reached")

func (s *backendSlot) isUnThrottleAvailableBySpentTime() bool {
	return s.lastUnThrottle.Load()+int64(time.Minute) > time.Now().UnixNano()
}

func (s *backendSlot) unthrottle() (newRate, newRatePercents int, err error) {
	for {
		lim := s.rate.Load()
		old := s.throttles.Load()
		if s.throttles.Load() <= minUnthrottles {
			return int(lim.Limit()), lim.Burst(), MinUnthrottlesReached
		}

		if s.throttles.CompareAndSwap(old, old-1) {
			or := s.backend.Cfg().Rate
			rt := or / 10 * ((int(old) - 1) - 10)
			bt := rt/2 + 1
			s.rate.Store(rate.NewLimiter(s.rate.Load().Ctx(), rt, bt))
			s.lastUnThrottle.Store(time.Now().UnixNano())
			return rt, bt, nil
		}
	}
}

func (s *backendSlot) promote() {
	s.state.Store(uint32(stateHealthy))
	s.idleHealthCheckFailsInARow.Store(0)
	s.throttles.Store(0)
	s.reqCount.Store(0)
	s.errCount.Store(0)
	s.sickedAt.Store(0)
	s.deadAt.Store(0)
}

func (s *backendSlot) quarantine() {
	s.sickedAt.Store(time.Now().UnixNano())
}

func (s *backendSlot) kill() {
	s.deadAt.Store(time.Now().UnixNano())
}

func (s *backendSlot) resurrect() {
	s.promote()
}

func (s *backendSlot) bury() {
	// no-op: fully removed externally
}
