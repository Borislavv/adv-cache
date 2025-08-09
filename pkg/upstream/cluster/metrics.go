
package cluster

import (
	"bytes"
	"fmt"
	"time"
)

// SnapshotMetrics renders Prometheus exposition format with aggregate gauges and per-backend stats.
func (c *Cluster) SnapshotMetrics() []byte {
	var b bytes.Buffer

	// aggregate
	var healthyN, sickN, deadN int
	for _, s := range c.all {
		switch State(s.state.Load()) {
		case Healthy:
			healthyN++
		case Sick:
			sickN++
		case Dead:
			deadN++
		}
	}
	fmt.Fprintf(&b, "cluster_backends_healthy %d\n", healthyN)
	fmt.Fprintf(&b, "cluster_backends_sick %d\n", sickN)
	fmt.Fprintf(&b, "cluster_backends_dead %d\n", deadN)

	// per-backend
	now := time.Now().Unix()
	for name, s := range c.all {
		fmt.Fprintf(&b, "backend_state{backend=%q} %q\n", name, State(s.state.Load()).String())
		req, fail := s.window10s()
		var er float64
		if req > 0 {
			er = float64(fail) / float64(req)
		}
		fmt.Fprintf(&b, "backend_req_10s{backend=%q} %d\n", name, req)
		fmt.Fprintf(&b, "backend_err_10s{backend=%q} %d\n", name, fail)
		fmt.Fprintf(&b, "backend_error_rate_10s{backend=%q} %f\n", name, er)
		fmt.Fprintf(&b, "backend_rps_limit{backend=%q} %d\n", name, s.effective.Load())
		ew := s.ewmaNanos.Load()
		if ew > 0 {
			fmt.Fprintf(&b, "backend_latency_ewma_seconds{backend=%q} %f %d\n", name, float64(ew)/1e9, now*1000)
		}
	}
	return b.Bytes()
}
