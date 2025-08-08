package upstream

import (
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
	backend     Backend
	rateLimiter *ThrottleLimiter

	reqCount     atomic.Uint32
	errCount     atomic.Uint32
	consecFails  atomic.Uint32
	idleChecks   atomic.Uint32
	throttles    atomic.Uint32
	lastThrottle atomic.Int64

	state    atomic.Uint32
	sickedAt atomic.Int64 // UnixNano
	deadAt   atomic.Int64 // UnixNano
}

func newBackendSlot(b Backend) *backendSlot {
	rt := uint32(b.Cfg().Rate)
	return &backendSlot{
		backend:      b,
		reqCount:     atomic.Uint32{},
		errCount:     atomic.Uint32{},
		consecFails:  atomic.Uint32{},
		throttles:    atomic.Uint32{},
		idleChecks:   atomic.Uint32{},
		lastThrottle: atomic.Int64{},
		sickedAt:     atomic.Int64{},
		deadAt:       atomic.Int64{},
		rateLimiter:  NewThrottleLimiter(rt, rt/10),
	}
}

func (s *backendSlot) Track(err error) {
	s.reqCount.Add(1)
	if err != nil {
		s.errCount.Add(1)
		s.consecFails.Add(1)
	} else {
		s.consecFails.Store(0)
	}
}

func (s *backendSlot) getState() backendState {
	return backendState(s.state.Load())
}

func (s *backendSlot) casState(from, to backendState) bool {
	return s.state.CompareAndSwap(uint32(from), uint32(to))
}

func (s *backendSlot) promote() {
	s.state.Store(uint32(stateHealthy))
	s.reqCount.Store(0)
	s.errCount.Store(0)
	s.consecFails.Store(0)
	s.throttles.Store(0)
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
