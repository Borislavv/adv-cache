package ctime

import (
	"sync/atomic"
	"time"
)

var nowUnix atomic.Int64

func Start(resolution time.Duration) func() {
	nowUnix.Store(time.Now().UnixNano())
	t := time.NewTicker(resolution)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case tt := <-t.C:
				nowUnix.Store(tt.UnixNano())
			case <-done:
				t.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}
func Now() time.Time                  { return time.Unix(0, nowUnix.Load()) }
func UnixNano() int64                 { return nowUnix.Load() }
func Since(t time.Time) time.Duration { return Now().Sub(t) }
