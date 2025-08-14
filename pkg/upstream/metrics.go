package upstream

import (
	"fmt"
	"github.com/VictoriaMetrics/metrics"
)

type backendMetrics struct {
	// movement
	Quarantined, Cured, Killed, Resurrected, Buried *metrics.Counter

	// req/err
	Requests, Errors *metrics.Counter
	ErrorRate        *metrics.Gauge

	// probes
	ProbeOK, ProbeFail *metrics.Counter
	// простая версия задержек: sum/count
	ProbeDurSum, ProbeDurCount *metrics.Counter

	// throttle/limits
	Throttled, Unthrottled *metrics.Counter
	TargetRPS, CurrentRPS  *metrics.Gauge
	ThrottleLevel          *metrics.Gauge
}

func newBackendMetrics(id string) *backendMetrics {
	// Важно: форматируй ЛЕЙБЛЫ один раз тут; далее — только Inc/Set.
	lab := func(name string) string {
		return fmt.Sprintf(`%s{backend=%q}`, name, id)
	}
	return &backendMetrics{
		Quarantined: metrics.GetOrCreateCounter(lab("adv_backend_quarantined_total")),
		Cured:       metrics.GetOrCreateCounter(lab("adv_backend_cured_total")),
		Killed:      metrics.GetOrCreateCounter(lab("adv_backend_killed_total")),
		Resurrected: metrics.GetOrCreateCounter(lab("adv_backend_resurrected_total")),
		Buried:      metrics.GetOrCreateCounter(lab("adv_backend_buried_total")),

		Requests:  metrics.GetOrCreateCounter(lab("adv_backend_requests_total")),
		Errors:    metrics.GetOrCreateCounter(lab("adv_backend_errors_total")),
		ErrorRate: metrics.GetOrCreateGauge(lab("adv_backend_error_rate")),

		ProbeOK:       metrics.GetOrCreateCounter(lab("adv_backend_probes_success_total")),
		ProbeFail:     metrics.GetOrCreateCounter(lab("adv_backend_probes_failed_total")),
		ProbeDurSum:   metrics.GetOrCreateCounter(lab("adv_backend_probe_duration_seconds_sum")),
		ProbeDurCount: metrics.GetOrCreateCounter(lab("adv_backend_probe_duration_seconds_count")),

		Throttled:     metrics.GetOrCreateCounter(lab("adv_backend_throttled_total")),
		Unthrottled:   metrics.GetOrCreateCounter(lab("adv_backend_unthrottled_total")),
		TargetRPS:     metrics.GetOrCreateGauge(lab("adv_backend_target_rps")),
		CurrentRPS:    metrics.GetOrCreateGauge(lab("adv_backend_current_rps")),
		ThrottleLevel: metrics.GetOrCreateGauge(lab("adv_backend_throttle_level")),
	}
}
