package utils

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/ctime"
	"time"
)

func NewTicker(ctx context.Context, interval time.Duration) (ch <-chan time.Time) {
	ctx, cancel := context.WithCancel(ctx)

	tickCh := make(chan time.Time, 1)
	tickCh <- ctime.Now()

	go func() {
		ticker := time.NewTicker(interval)
		defer func() {
			ticker.Stop()
			close(tickCh)
			cancel()
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				tickCh <- t
			}
		}
	}()

	return tickCh
}
