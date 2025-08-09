
package cluster

import (
	"sync/atomic"
)

// State represents backend lifecycle.
type State int32

const (
	Healthy State = iota
	Sick
	Dead
	Buried
)

func (s State) String() string {
	switch s {
	case Healthy:
		return "healthy"
	case Sick:
		return "sick"
	case Dead:
		return "dead"
	case Buried:
		return "buried"
	default:
		return "unknown"
	}
}

// casState performs atomic CAS transition. Returns false if already changed.
func casState(dst *atomic.Int32, from, to State) bool {
	return dst.CompareAndSwap(int32(from), int32(to))
}
