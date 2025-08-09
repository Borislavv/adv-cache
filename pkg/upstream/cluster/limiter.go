package cluster

import (
	"sync/atomic"
	"time"
)

// tokenLimiter is a simple lock-free token bucket tailored for 0 alloc hot-path.
// Refilled every tick of wall-second (monotonic via time.Now).
// Allows small burst (configured), and fast-fail when empty.
type tokenLimiter struct {
	rate     uint32       // tokens per second (steady)
	burst    uint32       // max bucket capacity
	nowNanos atomic.Int64 // unix nano
	tokens   atomic.Int64
}

func newTokenLimiter(rate, burst int) *tokenLimiter {
	if rate <= 0 {
		rate = 0
	}
	if burst < 1 {
		burst = 1
	}
	l := &tokenLimiter{rate: uint32(rate), burst: uint32(burst)}
	l.nowNanos.Store(time.Now().UnixNano())
	l.tokens.Store(int64(burst))
	return l
}

// allow tries to consume one token.
// It never allocates and never blocks. Returns true if permitted.
func (l *tokenLimiter) allow(nanos int64) bool {
	if l.rate == 0 {
		// disabled limiter => always allow
		return true
	}
	last := l.nowNanos.Load()
	if nanos != last {
		// try to advance the second and refill
		if l.nowNanos.CompareAndSwap(last, nanos) {
			// compute new tokens with min(capacity, tokens + rate*(delta))
			delta := nanos - last
			add := int64(uint64(l.rate) * uint64(delta))
			for {
				cur := l.tokens.Load()
				n := cur + add
				cap := int64(l.burst)
				if n > cap {
					n = cap
				}
				if l.tokens.CompareAndSwap(cur, n) {
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
