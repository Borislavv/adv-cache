package model

import (
	"math"
	"sync/atomic"
)

// Entry state bit layout in one uint64:
// [32-bit version][1-bit isDoomed][31-bit refCount]
// Version in high 32 bits, isDoomed in bit 31, refCount in low 31 bits.
// We reserve version == MaxUint32 to signal overflow/ABA protection.

const (
	// FinalizingRightNowRefCountValue is the special refCount value indicating finalization in progress
	FinalizingRightNowRefCountValue = 1<<31 - 1
	// MaxRefCount is the maximum normal reference count before hitting the reserved FinalizingRightNowRefCountValue
	MaxRefCount = FinalizingRightNowRefCountValue - 1

	// Masks for e.Une.Packing (unexported)
	maskRefCount = uint64(FinalizingRightNowRefCountValue) // lower 31 bits (including reserved)
	maskIsDoomed = uint64(1) << 31                         // bit 31
)

// Pack combines version, isDoomed flag, and refCount into one uint64.
// Panics if version or refCount exceed allowed ranges.
// Note: version == MaxUint32 is reserved to prevent wrap-around and ABA issues.
func (e *Entry) Pack(version uint32, isDoomed bool, refCount uint32) uint64 {
	// Prevent version wrap-around: reserve MaxUint32 for overflow/ABA
	if version >= math.MaxUint32 {
		panic("e.Pack: version overflow or reserved value")
	}
	// Validate refCount: allow 0..MaxRefCount, or exactly FinalizingRightNowRefCountValue when isDoomed
	if refCount > FinalizingRightNowRefCountValue {
		panic("e.Pack: refCount overflow")
	}
	// Counter is overflow without doomedTrueValue flag
	if !isDoomed && refCount > MaxRefCount {
		panic("Pack: refCount overflow")
	}
	// Attempt to set up reserved value without doomedTrueValue flag
	if isDoomed && refCount != FinalizingRightNowRefCountValue {
		panic("Pack: reserved refCount misuse")
	}
	// Assemble state bits
	state := (uint64(version) << 32) | uint64(refCount)
	if isDoomed {
		state |= maskIsDoomed
	}
	return state
}

// Unpack splits state into version, isDoomed flag, and refCount.
func (e *Entry) Unpack() (oldState uint64, version uint32, isDoomed bool, refCount uint32) {
	oldState = atomic.LoadUint64(&e.state)
	version = uint32(oldState >> 32)
	isDoomed = (oldState & maskIsDoomed) != 0
	refCount = uint32(oldState & maskRefCount)
	return
}

func (e *Entry) unpack(state uint64) (version uint32, isDoomed bool, refCount uint32) {
	version = uint32(state >> 32)
	isDoomed = (state & maskIsDoomed) != 0
	refCount = uint32(state & maskRefCount)
	return
}

func (e *Entry) Version() uint32 {
	_, v, _, _ := e.Unpack()
	return v
}

// IncVersion returns a new state with version incremented by 1.
// Panics on version overflow.
func (e *Entry) IncVersion(state uint64) uint64 {
	ver, isDoomed, ref := e.unpack(state)
	return e.Pack(ver+1, isDoomed, ref)
}

// IncRefCount attempts to increment refCount by 1.
// Returns (newState, true) on success, (oldState, false) if isDoomed==true or refCount>=MaxRefCount.
// Note: isDoomed flag is cleared only in the success case.
func (e *Entry) IncRefCount(state uint64) (uint64, bool) {
	ver, isDoomed, ref := e.unpack(state)
	if isDoomed || ref >= MaxRefCount {
		return state, false
	}
	// On success, clear isDoomed and increase refCount
	return e.Pack(ver, isDoomed, ref+1), true
}

// DecRefCount attempts to decrement refCount by 1.
// Returns (newState, true) on success, (oldState, false) if refCount==0 or refCount==FinalizingRightNowRefCountValue.
// isDoomed flag is preserved.
func (e *Entry) DecRefCount(state uint64) (uint64, bool) {
	ver, isDoomed, ref := e.unpack(state)
	// cannot decrement if no references or if in finalizing state
	if ref == 0 || ref == FinalizingRightNowRefCountValue {
		return state, false
	}
	// On success, preserve isDoomed, decrement refCount
	return e.Pack(ver, isDoomed, ref-1), true
}

// MarkAsDoomed sets isDoomed flag without changing refCount or version.
func (e *Entry) MarkAsDoomed(state uint64) uint64 {
	ver, _, ref := e.unpack(state)
	return e.Pack(ver, true, ref)
}

// MarkAsFinalizable sets refCount to the reserved FinalizingRightNowRefCountValue and isDoomed=true.
func (e *Entry) MarkAsFinalizable(state uint64) uint64 {
	ver, _, _ := e.unpack(state)
	return e.Pack(ver+1, true, FinalizingRightNowRefCountValue)
}

// ResetState bumps version by 1, clears isDoomed and refCount to zero.
// Panics on version overflow.
func (e *Entry) ResetState(state uint64) uint64 {
	ver, _, _ := e.unpack(state)
	return e.Pack(ver+1, false, 0)
}

func (e *Entry) IsAcquirable(expectedVersion uint32) (oldState uint64, isAcquirable bool) {
	state := atomic.LoadUint64(&e.state)
	version, isDoomed, refCount := e.unpack(state)
	return state, version != expectedVersion || isDoomed || refCount <= FinalizingRightNowRefCountValue
}

func (e *Entry) IsFinalizing(refCount uint32) (isFinalizingInProgress bool) {
	return refCount <= FinalizingRightNowRefCountValue
}

func (e *Entry) NeedFinalize(isDoomed bool, refCount uint32) bool {
	return isDoomed && refCount == preDoomedRefCount
}
