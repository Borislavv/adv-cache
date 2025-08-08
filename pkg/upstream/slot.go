package upstream

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/rate"
	"sync/atomic"
)

type slotState uint32

const (
	stateInvalid slotState = iota
	stateHealthy
	stateSick
	stateDead
)

func (s slotState) String() string {
	switch s {
	case stateHealthy:
		return "healthy"
	case stateSick:
		return "sick"
	case stateDead:
		return "dead"
	default:
		return "invalid"
	}
}

type backendSlot struct {
	backend   Backend
	state     atomic.Uint32
	rate      rate.Limiter
	hcRetries atomic.Int32
}

func newBackendSlot(ctx context.Context, backend Backend) *backendSlot {
	rt := backend.Cfg().Rate
	if rt < 1 {
		rt = 1
	}
	bt := backend.Cfg().Rate / 10
	if bt < 1 {
		bt = 1
	}
	return &backendSlot{
		backend: backend,
		rate:    *rate.NewLimiter(ctx, rt, bt),
	}
}

func (s *backendSlot) State() slotState {
	return slotState(s.state.Load())
}

func (s *backendSlot) TryTransition(from, to slotState) bool {
	return s.state.CompareAndSwap(uint32(from), uint32(to))
}

func (s *backendSlot) MarkDead() {
	s.state.Store(uint32(stateDead))
}
