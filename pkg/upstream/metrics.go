package upstream

import "sync/atomic"

// -----------------------------------------------------------------------------
// Aggregate metrics.
// -----------------------------------------------------------------------------

var (
	healthyGauge   atomic.Int64
	sickGauge      atomic.Int64
	deadGauge      atomic.Int64
	forgottenGauge atomic.Int64
)

func HealthyBackends() int64 { return healthyGauge.Load() }
func SickBackends() int64    { return sickGauge.Load() }
func DeadBackends() int64    { return deadGauge.Load() }
