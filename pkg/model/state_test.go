package model

//
//import (
//	"math"
//	"testing"
//)
//
//func TestPackUnpackRoundtrip(t *testing.T) {
//	cases := []struct {
//		version  uint32
//		isDoomed bool
//		refCount uint32
//	}{{
//		version: 0, isDoomed: false, refCount: 0,
//	}, {
//		version: 1, isDoomed: false, refCount: 42,
//	}, {
//		version: 12345, isDoomed: true, refCount: 10,
//	}, {
//		version: math.MaxUint32 - 1, isDoomed: true, refCount: FinalizingRightNowRefCountValue,
//	}}
//
//	for _, c := range cases {
//		state := Pack(c.version, c.isDoomed, c.refCount)
//		v2, doomed2, rc2 := Unpack(state)
//		if v2 != c.version {
//			t.Errorf("version mismatch: got %d, want %d", v2, c.version)
//		}
//		if doomed2 != c.isDoomed {
//			t.Errorf("isDoomed mismatch: got %v, want %v", doomed2, c.isDoomed)
//		}
//		if rc2 != c.refCount {
//			t.Errorf("refCount mismatch: got %d, want %d", rc2, c.refCount)
//		}
//	}
//}
//
//func TestIncVersionPanics(t *testing.T) {
//	state := Pack(math.MaxUint32-1, false, 0)
//	// next IncVersion should panic
//	defer func() {
//		if r := recover(); r == nil {
//			t.Error("expected panic on version overflow")
//		}
//	}()
//	_ = IncVersion(state)
//}
//
//func TestIncRefCount(t *testing.T) {
//	// normal increment
//	state := Pack(0, false, 0)
//	newState, ok := IncRefCount(state)
//	if !ok {
//		t.Error("IncRefCount failed on valid state")
//	}
//	_, doomed, rc := Unpack(newState)
//	if doomed {
//		t.Error("isDoomed flag incorrectly set after IncRefCount")
//	}
//	if rc != 1 {
//		t.Errorf("refCount = %d, want 1", rc)
//	}
//
//	// already doomedTrueValue
//	state = MarkAsDoomed(Pack(0, false, 5))
//	_, ok = IncRefCount(state)
//	if ok {
//		t.Error("IncRefCount should fail when isDoomed=true")
//	}
//
//	// at maxRefCount
//	state = Pack(0, false, MaxRefCount)
//	_, ok = IncRefCount(state)
//	if ok {
//		t.Error("IncRefCount should fail at maxRefCount")
//	}
//}
//
//func TestMarkAsDoomedAndFinalizable(t *testing.T) {
//	state := Pack(5, false, 3)
//	doomedState := MarkAsDoomed(state)
//	v, doomed, rc := Unpack(doomedState)
//	if !doomed {
//		t.Error("expected isDoomed=true")
//	}
//	if v != 5 || rc != 3 {
//		t.Errorf("unexpected values after MarkAsDoomed: v=%d, rc=%d", v, rc)
//	}
//
//	finalState := MarkAsFinalizable(state)
//	v2, doomed2, rc2 := Unpack(finalState)
//	if !doomed2 || rc2 != FinalizingRightNowRefCountValue {
//		t.Errorf("after MarkAsFinalizable: isDoomed=%v, refCount=%d", doomed2, rc2)
//	}
//	if v2 != 5 {
//		t.Errorf("version changed after MarkAsFinalizable: got %d, want 5", v2)
//	}
//}
//
//func TestResetState(t *testing.T) {
//	state := Pack(10, true, 7)
//	reset := ResetState(state)
//	v, doomed, rc := Unpack(reset)
//	if v != 11 || doomed || rc != 0 {
//		t.Errorf("unexpected reset state: v=%d, doomedTrueValue=%v, rc=%d", v, doomed, rc)
//	}
//	// Test panic on overflow
//	state2 := Pack(math.MaxUint32-1, false, 0)
//	defer func() {
//		if r := recover(); r == nil {
//			t.Error("expected panic on ResetState version overflow")
//		}
//	}()
//	_ = ResetState(state2)
//}
