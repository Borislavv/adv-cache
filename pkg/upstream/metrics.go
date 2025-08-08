package upstream

import (
	"fmt"
	"sync/atomic"
)

var (
	healthyGauge atomic.Int64
	sickGauge    atomic.Int64
	deadGauge    atomic.Int64
	buriedGauge  atomic.Int64
)

func SetGaugeHealthy(n int) { healthyGauge.Store(int64(n)) }
func IncGaugeHealthy()      { healthyGauge.Add(1) }
func DecGaugeHealthy()      { healthyGauge.Add(-1) }

func IncGaugeSick()   { sickGauge.Add(1) }
func DecGaugeSick()   { sickGauge.Add(-1) }
func IncGaugeDead()   { deadGauge.Add(1) }
func DecGaugeDead()   { deadGauge.Add(-1) }
func IncGaugeBuried() { buriedGauge.Add(1) }

func SnapshotMetrics() string {
	return fmt.Sprintf(`# HELP upstream_backends Number of backends by state
		# TYPE upstream_backends gauge
		upstream_backends{state="healthy"} %d
		upstream_backends{state="sick"} %d
		upstream_backends{state="dead"} %d
		upstream_backends{state="buried"} %d
	`,
		healthyGauge.Load(),
		sickGauge.Load(),
		deadGauge.Load(),
		buriedGauge.Load(),
	)
}
