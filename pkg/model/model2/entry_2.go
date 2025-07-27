package model2

import (
	"sync/atomic"
)

const (
	doomedRefCount = (1 << 31) - 1 // special reserved value: entry is being finalized
	maxRefCount    = doomedRefCount - 1

	maskRefCount = (1 << 31) - 1    // lower 31 bits
	maskIsDoomed = 1 << 31          // bit 31
	maskVersion  = ^uint64(0) << 32 // top 32 bits
)

type Entry struct {
	state uint64 // [32-bit version][1-bit isDoomed][31-bit refCount]
}

func packState(version uint32, isDoomed bool, refCount uint32) uint64 {
	if refCount >= doomedRefCount {
		// fallback: reset state to prevent overflow
		version++
		refCount = 0
		isDoomed = false
	}
	state := (uint64(version) << 32) | uint64(refCount)
	if isDoomed {
		state |= maskIsDoomed
	}
	return state
}

func unpackState(state uint64) (version uint32, isDoomed bool, refCount uint32) {
	version = uint32(state >> 32)
	isDoomed = (state & maskIsDoomed) != 0
	refCount = uint32(state & maskRefCount)
	return
}

func (e *Entry) Acquire(expectedVersion uint32) bool {
	for {
		old := atomic.LoadUint64(&e.state)
		ver, doomed, ref := unpackState(old)

		if ver != expectedVersion || doomed || ref >= maxRefCount {
			return false
		}

		newState := packState(ver, false, ref+1)
		if atomic.CompareAndSwapUint64(&e.state, old, newState) {
			return true
		}
	}
}

func (e *Entry) Release() (finalized bool) {
	for {
		old := atomic.LoadUint64(&e.state)
		ver, doomed, ref := unpackState(old)

		if ref == 0 || ref == doomedRefCount {
			return false
		}

		newRef := ref - 1
		newState := packState(ver, doomed, newRef)
		needFinalize := newRef == 0 && doomed

		if atomic.CompareAndSwapUint64(&e.state, old, newState) {
			if needFinalize {
				e.tryFinalize()
				return true
			}
			return false
		}
	}
}

func (e *Entry) Remove() (finalized bool) {
	for {
		old := atomic.LoadUint64(&e.state)
		ver, doomed, ref := unpackState(old)

		if doomed && ref == 0 {
			return false
		}

		newState := packState(ver, true, ref)
		if atomic.CompareAndSwapUint64(&e.state, old, newState) {
			return e.Release()
		}
	}
}

func (e *Entry) tryFinalize() {
	for {
		old := atomic.LoadUint64(&e.state)
		ver, _, _ := unpackState(old)

		// Set refCount to doomedRefCount to block future Acquire/Release
		doomedState := packState(ver, true, doomedRefCount)
		if atomic.CompareAndSwapUint64(&e.state, old, doomedState) {
			e.finalize()
			return
		}

		cur := atomic.LoadUint64(&e.state)
		newVer, _, newRef := unpackState(cur)
		if newVer > ver || newRef == doomedRefCount {
			return
		}
	}
}

func (e *Entry) finalize() {
	for {
		old := atomic.LoadUint64(&e.state)
		ver, _, _ := unpackState(old)
		newState := packState(ver+1, false, 0)
		if atomic.CompareAndSwapUint64(&e.state, old, newState) {
			e.cleanup()
			return
		}
	}
}

func (e *Entry) cleanup() {
	// TODO: zero out all fields, return to pool etc.
}
