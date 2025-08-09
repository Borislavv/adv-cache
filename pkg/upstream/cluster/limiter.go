
package cluster

import (
	"sync/atomic"
	"time"
)

// tokenLimiter is a simple lock-free token bucket tailored for 0 alloc hot-path.
// Refilled every tick of wall-second (monotonic via time.Now).
// Allows small burst (configured), and fast-fail when empty.
type tokenLimiter struct {
	rate   uint32 // tokens per second (steady)
	burst  uint32 // max bucket capacity
	nowSec atomic.Int64
	tokens atomic.Int64
}

func newTokenLimiter(rate, burst int) *tokenLimiter {
	if rate <= 0 {
		rate = 0
	}
	if burst < 1 {
		burst = 1
	}
	l := &tokenLimiter{rate: uint32(rate), burst: uint32(burst)}
	sec := time.Now().Unix()
	l.nowSec.Store(sec)
	l.tokens.Store(int64(burst))
	return l
}

// allow tries to consume one token.
// It never allocates and never blocks. Returns true if permitted.
func (l *tokenLimiter) allow(now time.Time) bool {
	if l.rate == 0 {
		// disabled limiter => always allow
		return true
	}
	sec := now.Unix()
	last := l.nowSec.Load()
	if sec != last {
		// try to advance the second and refill
		if l.nowSec.CompareAndSwap(last, sec) {
			// compute new tokens with min(capacity, tokens + rate*(delta))
			delta := sec - last
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
