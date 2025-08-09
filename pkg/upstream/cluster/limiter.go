package cluster

import (
	"sync/atomic"
	"time"
)

// tokenLimiter is a simple lock-free token bucket tailored for 0 alloc hot-path.
// Refilled every tick of wall-second (monotonic via time.Now).
// Allows small burst (configured), and fast-fail when empty.
type tokenLimiter struct {
	rate     atomic.Uint32 // tokens per second (steady)
	burst    atomic.Uint32 // max bucket capacity
	nowNanos atomic.Int64  // unix nano
	tokens   atomic.Int64
}

func newTokenLimiter(rate, burst int) *tokenLimiter {
	if rate <= 0 {
		rate = 0
	}
	if burst < 1 {
		burst = 1
	}

	l := &tokenLimiter{rate: atomic.Uint32{}, burst: atomic.Uint32{}}
	l.nowNanos.Store(time.Now().UnixNano())
	l.tokens.Store(int64(burst))
	l.rate.Store(uint32(rate))
	l.burst.Store(uint32(burst))

	return l
}

// allow tries to consume one token.
// It never allocates and never blocks. Returns true if permitted.
func (l *tokenLimiter) allow(nanos int64) bool {
	if l.rate.Load() == 0 {
		// disabled limiter => always allow
		return true
	}
	last := l.nowNanos.Load()
	if nanos != last {
		// try to advance the second and refill
		if l.nowNanos.CompareAndSwap(last, nanos) {
			// compute new tokens with min(capacity, tokens + rate*(delta))
			delta := nanos - last
			add := int64(uint64(l.rate.Load()) * uint64(delta))
			for {
				oldNum := l.tokens.Load()
				newNum := oldNum + add
				if l.tokens.CompareAndSwap(oldNum, newNum) {
					break
				}
			}
		}
	}
	for {
		cur := l.tokens.Load()
		if cur <= 0 {
			return false
		}
		if l.tokens.CompareAndSwap(cur, cur-1) {
			return true
		}
	}
}
