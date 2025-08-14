package upstream

import (
	"fmt"
	"github.com/VictoriaMetrics/metrics"
	"github.com/rs/zerolog/log"
	"time"
)

type backendMetrics struct {
	HealthyState, SickState, DeadState, BuriedState *metrics.Gauge

	// movement
	Quarantined, Cured, Killed, Resurrected, Buried *metrics.Counter

	// req/err
	Requests, Errors *metrics.Counter
	ErrorRate        *metrics.Gauge

	// probes
	ProbeOK, ProbeFail *metrics.Counter

	// throttle/limits
	Throttled, Unthrottled *metrics.Counter
	ThrottleLevel          *metrics.Gauge

	// load
	CurrentRateLimit *metrics.Counter
	CurrentRPS       *metrics.Gauge
}

func newBackendMetrics(id string) *backendMetrics {
	lab := func(name string) string {
		return fmt.Sprintf(`%s{backend=%q}`, name, id)
	}
	return &backendMetrics{
		HealthyState: metrics.NewGauge(lab("adv_backend_is_healthy"), nil),
		SickState:    metrics.NewGauge(lab("adv_backend_is_sick"), nil),
		DeadState:    metrics.NewGauge(lab("adv_backend_is_dead"), nil),
		BuriedState:  metrics.NewGauge(lab("adv_backend_is_buried"), nil),

		Quarantined: metrics.GetOrCreateCounter(lab("adv_backend_quarantined_total")),
		Cured:       metrics.GetOrCreateCounter(lab("adv_backend_cured_total")),
		Killed:      metrics.GetOrCreateCounter(lab("adv_backend_killed_total")),
		Resurrected: metrics.GetOrCreateCounter(lab("adv_backend_resurrected_total")),
		Buried:      metrics.GetOrCreateCounter(lab("adv_backend_buried_total")),

		Requests:  metrics.GetOrCreateCounter(lab("adv_backend_requests_total")),
		Errors:    metrics.GetOrCreateCounter(lab("adv_backend_errors_total")),
		ErrorRate: metrics.NewGauge(lab("adv_backend_error_rate"), nil),

		ProbeOK:   metrics.GetOrCreateCounter(lab("adv_backend_probes_success_total")),
		ProbeFail: metrics.GetOrCreateCounter(lab("adv_backend_probes_failed_total")),

		CurrentRateLimit: metrics.GetOrCreateCounter(lab("adv_backend_rate_limit_total")),
		Throttled:        metrics.GetOrCreateCounter(lab("adv_backend_throttled_total")),
		Unthrottled:      metrics.GetOrCreateCounter(lab("adv_backend_unthrottled_total")),
		ThrottleLevel:    metrics.GetOrCreateGauge(lab("adv_backend_throttle_level"), nil),

		CurrentRPS: metrics.GetOrCreateGauge(lab("adv_backend_current_rps"), nil),
	}
}

func (c *BackendCluster) writeMetrics(slot *backendSlot, interval time.Duration) {
	total := slot.hotPathCounters.total.Load()
	errors := slot.hotPathCounters.errors.Load()

	errRate := safeDivide(float64(errors), float64(total)) * 100
	rps := float64(total) / interval.Seconds()
	slot.metrics.ProbeOK.Set(uint64(slot.counters.sucProbes))
	slot.metrics.ProbeFail.Set(uint64(slot.counters.errProbes))
	slot.metrics.Requests.Set(uint64(total))
	slot.metrics.Errors.Set(uint64(errors))
	slot.metrics.ErrorRate.Set(float64(errors) / float64(total))
	slot.metrics.CurrentRPS.Set(rps)
	slot.metrics.CurrentRateLimit.Set(uint64(slot.jitter.limit))

	log.Info().Msgf(
		"[upstream][%s] %s={probes: {succes=%d, error=%d}, rate: {err=%.f%%, total=%d, errors=%d, limit=%dreqs/s, RPS=%.f}}",
		slot.state.String(), slot.upstream.backend.id, slot.counters.sucProbes, slot.counters.errProbes,
		errRate, total, errors, slot.jitter.limit, rps,
	)
}

func (m *backendMetrics) cure() {
	m.Cured.Add(1)
	m.HealthyState.Set(1)
	m.SickState.Set(0)
	m.DeadState.Set(0)
	m.BuriedState.Set(0)
}

func (m *backendMetrics) quarantine() {
	m.Quarantined.Add(1)
	m.HealthyState.Set(0)
	m.SickState.Set(1)
	m.DeadState.Set(0)
	m.BuriedState.Set(0)
}

func (m *backendMetrics) kill() {
	m.Killed.Add(1)
	m.HealthyState.Set(0)
	m.SickState.Set(0)
	m.DeadState.Set(1)
	m.BuriedState.Set(0)
}

func (m *backendMetrics) bury() {
	m.HealthyState.Set(0)
	m.SickState.Set(0)
	m.DeadState.Set(0)
	m.BuriedState.Set(1)
}

func (m *backendMetrics) resurrect() {
	m.Resurrected.Add(1)
	m.HealthyState.Set(0)
	m.SickState.Set(1)
	m.DeadState.Set(0)
	m.BuriedState.Set(0)
}

func (m *backendMetrics) throttle(throttles float64) {
	m.Throttled.Add(1)
	m.ThrottleLevel.Set(throttles)
}

func (m *backendMetrics) unthrottle(throttles float64) {
	m.Unthrottled.Add(1)
	m.ThrottleLevel.Set(throttles)
}
