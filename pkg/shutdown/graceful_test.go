package shutdown

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGraceful_ShutdownWithoutTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	g := NewGraceful(ctx, cancel)
	g.SetGracefulTimeout(100 * time.Millisecond)

	g.Add(1)
	go func() {
		defer g.Done()
		time.Sleep(10 * time.Millisecond)
	}()

	doneCh := make(chan error)
	go func() { doneCh <- g.ListenCancelAndAwait() }()
	time.Sleep(5 * time.Millisecond)
	cancel()

	assert.NoError(t, <-doneCh)
}
