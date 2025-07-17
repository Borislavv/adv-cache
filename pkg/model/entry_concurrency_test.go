package model

import (
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefCounting(t *testing.T) {
	var (
		mu             = &sync.Mutex{}
		db             = make(map[int]*Entry, 155)
		closeCh        = make(chan struct{})
		acquired       atomic.Int64
		notAcquired    atomic.Int64
		removed        atomic.Int64
		removeAttempts atomic.Int64
	)

	for i := 0; i <= 150; i++ {
		db[i] = newEntry()
	}

	wg := sync.WaitGroup{}
	wg.Add(4)

	go func() {
		time.Sleep(10 * time.Second)
		close(closeCh)
		wg.Done()
	}()

	// Reader
	go func() {
		defer wg.Done()
		for {
			select {
			case <-closeCh:
				return
			default:
				idx := rand.Intn(150)
				mu.Lock()
				if e := db[idx]; e.Acquire() {
					mu.Unlock()
					if e.payload.Load() == nil {
						panic("payload is nil")
					}
					if _, wasFinalized := e.Release(); wasFinalized {
						removed.Add(1)
					}
					acquired.Add(1)
				} else {
					mu.Unlock()
					notAcquired.Add(1)
				}
			}
		}
	}()

	// Writer
	go func() {
		defer wg.Done()
		for {
			select {
			case <-closeCh:
				return
			default:
				idx := rand.Intn(150)
				mu.Lock()
				if e, ok := db[idx]; ok {
					delete(db, idx)
					if e.Acquire() {
						if _, wasFinalized := e.Remove(); wasFinalized {
							removed.Add(1)
						}
						acquired.Add(1)
						removeAttempts.Add(1)
					} else {
						notAcquired.Add(1)
					}
				}
				db[idx] = newEntry()
				mu.Unlock()
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	// Writer
	go func() {
		defer wg.Done()
		for {
			select {
			case <-closeCh:
				return
			default:
				idx := rand.Intn(150)
				mu.Lock()
				if e, ok := db[idx]; ok {
					delete(db, idx)
					if e.Acquire() {
						if _, wasFinalized := e.Remove(); wasFinalized {
							removed.Add(1)
						}
						acquired.Add(1)
						removeAttempts.Add(1)
					} else {
						notAcquired.Add(1)
					}
				}
				db[idx] = newEntry()
				mu.Unlock()
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	// Writer
	go func() {
		defer wg.Done()
		for {
			select {
			case <-closeCh:
				return
			default:
				idx := rand.Intn(150)
				mu.Lock()
				if e, ok := db[idx]; ok {
					delete(db, idx)
					if e.Acquire() {
						if _, wasFinalized := e.Remove(); wasFinalized {
							removed.Add(1)
						}
						acquired.Add(1)
						removeAttempts.Add(1)
					} else {
						notAcquired.Add(1)
					}
				}
				db[idx] = newEntry()
				mu.Unlock()
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	wg.Wait()

	fmt.Printf("acquired: %d, notAcquired: %d, removed: %d, removeAttempts: %d\n",
		acquired.Load(), notAcquired.Load(), removed.Load(), removeAttempts.Load())

	if removed.Load() != removeAttempts.Load() {
		t.Fatalf("Mismatch: finalized %d, but remove attempts %d", removed.Load(), removeAttempts.Load())
	}
}

func newEntry() *Entry {
	return NewEntryFromField(0, 0, [16]byte{}, []byte(""), nil, func(rule *config.Rule, path []byte, query []byte, queryHeaders [][2][]byte) (status int, headers [][2][]byte, body []byte, releaseFn func(), err error) {
		return 0, nil, nil, nil, err
	}, 0, 0)
}
