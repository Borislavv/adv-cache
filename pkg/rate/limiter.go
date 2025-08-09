package rate

import (
	"context"
	"golang.org/x/time/rate"
)

type Limiter struct {
	ctx context.Context
	ch  chan struct{}
	*rate.Limiter
}

func NewLimiter(ctx context.Context, limit, burst int) *Limiter {
	l := &Limiter{
		ctx:     ctx,
		ch:      make(chan struct{}),
		Limiter: rate.NewLimiter(rate.Limit(limit), burst),
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				close(l.ch)
				return
			default:
				if err := l.Wait(ctx); err != nil {
					return
				}

				select {
				case <-ctx.Done():
					return
				case l.ch <- struct{}{}:
				}
			}
		}
	}()
	return l
}

func (l *Limiter) Ctx() context.Context {
	return l.ctx
}

func (l *Limiter) Chan() <-chan struct{} {
	return l.ch
}
