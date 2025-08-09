package cluster

import (
	"sync/atomic"
	"time"
)

// slot is immutable on hot path except small atomic fields.
type slot struct {
	be Backend

	// lifecycle
	state atomic.Int32 // State

	// rate-limit
	lim *tokenLimiter

	// moving window (10s) counters, lock-free via sharded seconds
	// win[i].ts stores unix second for slot i, so we can reuse 10 cells ring.
	win [10]secCounters

	// EWMA latency for slow-start and metrics; classic alpha smoothing.
	ewmaNanos atomic.Int64 // nanoseconds, 0 means uninitialized

	// effectiveRate is used by slow-start/restore: limiter.rate is target; effective is applied.
	effective atomic.Uint32 // current effective RPS applied by workers
}

type secCounters struct {
	sec  atomic.Int64 // unix second checkpoint for this cell
	req  atomic.Int64 // requests counted in that second
	fail atomic.Int64 // failures in that second
}

// recordOutcome is hot-path, allocation-free.
func (s *slot) recordOutcome(start int64, ok bool) {
	// EWMA latency update (lock-free single-writer assumption not guaranteed; use CAS loop)
	dur := time.Now().UnixNano() - start
	for {
		p := s.ewmaNanos.Load()
		if p == 0 {
			if s.ewmaNanos.CompareAndSwap(0, dur) {
				break
			}
			continue
		}
		// alpha=0.2 (~5 sample memory)
		n := p + (dur-p)/5
		if s.ewmaNanos.CompareAndSwap(p, n) {
			break
		}
	}

	// 10s sliding window counters
	idx := int(start % 10)
	cell := &s.win[idx]
	csec := cell.sec.Load()
	if csec != start {
		if cell.sec.CompareAndSwap(csec, start) {
			cell.req.Store(0)
			cell.fail.Store(0)
		}
	}
	cell.req.Add(1)
	if !ok {
		cell.fail.Add(1)
	}
}

func (s *slot) window10s() (req, fail int64) {
	now := time.Now().Unix()
	var r, f int64
	for i := 0; i < 10; i++ {
		if s.win[i].sec.Load() >= now-9 {
			r += s.win[i].req.Load()
			f += s.win[i].fail.Load()
		}
	}
	return r, f
}
