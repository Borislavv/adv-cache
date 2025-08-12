package rate

import (
	"context"
	"go.uber.org/ratelimit"
)

type Limiter struct {
	ch    chan struct{}
	l     ratelimit.Limiter
	limit int
}

func NewLimiter(ctx context.Context, limit int) *Limiter {
	limiter := &Limiter{
		limit: limit,
		ch:    make(chan struct{}),
		l:     ratelimit.New(limit),
	}
	go limiter.provider(ctx)
	return limiter
}

func (l *Limiter) provider(ctx context.Context) {
	defer close(l.ch)
	for {
		l.l.Take()
		select {
		case <-ctx.Done():
			return
		case l.ch <- struct{}{}:
		}
	}
}

func (l *Limiter) Take() {
	l.l.Take()
}

func (l *Limiter) Limit() int {
	return l.limit
}

func (l *Limiter) Chan() <-chan struct{} {
	return l.ch
}
