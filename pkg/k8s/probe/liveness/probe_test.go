package liveness

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type fakeService struct{ alive bool }

func (f *fakeService) IsAlive(ctx context.Context) bool { return f.alive }

func TestProbe_WatchAndToggle(t *testing.T) {
	svc := &fakeService{alive: true}
	probe := NewProbe(50 * time.Millisecond)
	probe.Watch(svc)

	assert.Eventually(t, probe.IsAlive, time.Second, 10*time.Millisecond)

	// change state
	svc.alive = false
	assert.Eventually(t, func() bool { return !probe.IsAlive() }, time.Second, 10*time.Millisecond)
}
