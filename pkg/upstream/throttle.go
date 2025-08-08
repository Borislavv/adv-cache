package upstream

import (
	"errors"
	"sync/atomic"
	"time"
)

var (
	errMaxThrottlesReached = errors.New("max throttling level reached")
)

type ThrottleLimiter struct {
	baseRate  uint32 // original configured rate
	current   atomic.Uint32
	burst     atomic.Uint32
	interval  atomic.Int64 // nanoseconds per token
	allowance atomic.Int64
	lastCheck atomic.Int64
}

func NewThrottleLimiter(rate, burst uint32) *ThrottleLimiter {
	if rate == 0 {
		rate = 1
	}
	if burst == 0 {
		burst = 1
	}
	interval := int64(time.Second / time.Duration(rate))

	l := &ThrottleLimiter{
		baseRate:  rate,
		burst:     atomic.Uint32{},
		interval:  atomic.Int64{},
		allowance: atomic.Int64{},
		lastCheck: atomic.Int64{},
	}
	l.current.Store(rate)
	l.burst.Store(burst)
	l.interval.Store(interval)
	return l
}

func (l *ThrottleLimiter) Throttle() bool {
	now := time.Now().UnixNano()
	last := l.lastCheck.Swap(now)
	elapsed := now - last
	if elapsed < 0 {
		elapsed = 0
	}

	interval := l.interval.Load()
	tokens := elapsed / interval
	if tokens > int64(l.burst.Load()) {
		tokens = int64(l.burst.Load())
	}

	allowance := l.allowance.Add(tokens)
	if allowance > int64(l.burst.Load()) {
		allowance = int64(l.burst.Load())
		l.allowance.Store(allowance)
	}

	if allowance <= 0 {
		return true
	}

	l.allowance.Add(-1)
	return false
}

func (l *ThrottleLimiter) Decrease() error {
	cur := l.current.Load()
	if cur <= l.baseRate/10 {
		return errMaxThrottlesReached
	}
	newRate := cur - l.baseRate/10
	if newRate < 1 {
		newRate = 1
	}
	l.current.Store(newRate)
	l.updateInterval(newRate)
	return nil
}

func (l *ThrottleLimiter) Increase() {
	cur := l.current.Load()
	if cur >= l.baseRate {
		return
	}
	newRate := cur + l.baseRate/10
	if newRate > l.baseRate {
		newRate = l.baseRate
	}
	l.current.Store(newRate)
	l.updateInterval(newRate)
}

func (l *ThrottleLimiter) updateInterval(rate uint32) {
	if rate == 0 {
		rate = 1
	}
	interval := int64(time.Second / time.Duration(rate))
	l.interval.Store(interval)
}
