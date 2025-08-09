package cluster

import (
	"context"
	"math"
	"math/rand"
	"time"

	"github.com/rs/zerolog/log"
)

// Workers implement asynchronous management. They never touch per-request allocations.

type WorkerCfg struct {
	ErrorRateThreshold float64       // default: 0.10
	SlowStartSeconds   int           // default: 10
	ProbeInterval      time.Duration // default: 2s
	DeadRetryBackoff   time.Duration // base backoff for dead -> healthy attempts (exp)
	JitterFrac         float64       // e.g. 0.2
}

func withDefaults() WorkerCfg {
	return WorkerCfg{
		ErrorRateThreshold: 0.10,
		SlowStartSeconds:   10,
		ProbeInterval:      2 * time.Second,
		DeadRetryBackoff:   3 * time.Second,
		JitterFrac:         0.2,
	}
}

func jitter(d time.Duration, frac float64) time.Duration {
	if frac <= 0 {
		return d
	}
	j := 1 + (rand.Float64()*2-1)*frac
	return time.Duration(float64(d) * j)
}

// runHealthyIdleMonitor: ensure healthy backends are really alive, refresh EWMA and break stalls.
func (c *Cluster) runHealthyIdleMonitor(ctx context.Context) {
	cfg := withDefaults()
	t := time.NewTicker(cfg.ProbeInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			hp := c.healthy.Load()
			if hp == nil {
				continue
			}
			for _, s := range *hp {
				if s.be.IsHealthy() != nil {
					_ = c.Quarantine(s.be.Name())
				}
			}
			// shuffle occasionally to avoid bias
			c.shuffleHealthy()
		}
	}
}

// runThrottleMonitor: compute 10s window error rate; quarantine if > threshold.
// also ramps up effective rate during slow-start by increasing limiter tokens.
func (c *Cluster) runThrottleMonitor(ctx context.Context) {
	cfg := withDefaults()
	t := time.NewTicker(time.Second)
	defer t.Stop()

	for ss := 0; ; ss++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now()
			hp := c.healthy.Load()
			if hp == nil {
				continue
			}
			for _, s := range *hp {
				req, fail := s.window10s()
				var rate float64
				if req > 0 {
					rate = float64(fail) / float64(req)
				}
				if rate > cfg.ErrorRateThreshold {
					log.Warn().Str("backend", s.be.Name()).Float64("error_rate_10s", rate).Msg("[upstream-cluster] outlier quarantine")
					_ = c.Quarantine(s.be.Name())
					continue
				}
				// slow-start: linearly increase effective limit for first SlowStartSeconds
				if ss < cfg.SlowStartSeconds {
					// raise tokens gradually
					target := s.be.Cfg().Rate
					cur := int(s.effective.Load())
					step := int(math.Max(1, float64(target)/float64(cfg.SlowStartSeconds)))
					n := cur + step
					if n > target {
						n = target
					}
					s.effective.Store(uint32(n))
					s.lim.rate = uint32(n)
					_ = now // ensure now is used to avoid inline skip
				}
			}
		}
	}
}

func (c *Cluster) runSickMonitor(ctx context.Context) {
	cfg := withDefaults()
	const requiredOK = 3 // подряд успешных проб для выхода из карантина

	t := time.NewTicker(cfg.ProbeInterval)
	defer t.Stop()

	// Локальная книга успешных подряд: имя -> счётчик
	successes := make(map[string]int, 64)

	// helper без аллокаций на fast-path (мы во воркере, аллокации допустимы)
	maxInt := func(a, b int) int {
		if a > b {
			return a
		}
		return b
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for name, s := range c.all {
				if State(s.state.Load()) != Sick {
					// Сбросим счётчик на всякий случай (если успели промоутить снаружи)
					delete(successes, name)
					continue
				}

				if s.be.IsHealthy() == nil {
					successes[name]++
					if successes[name] >= requiredOK {
						// Seed для slow-start: консервативная доля от target rate.
						target := s.be.Cfg().Rate
						// Стартуем где-то с ~10% или минимум 1 rps, чтобы не шарахнуть сразу.
						start := maxInt(1, int(math.Max(1, float64(target)/10.0)))

						// Устанавливаем мягкий старт до Promote, чтобы сразу после публикации лимитер не “взорвался”.
						s.effective.Store(uint32(start))
						s.lim.rate = uint32(start)

						if err := c.Promote(name); err == nil {
							log.Info().
								Str("backend", name).
								Int("seed_effective_rps", start).
								Int("target_rps", target).
								Msg("[upstream-cluster] promote from sick after stable probes (slow-start seeded)")
						} else {
							log.Debug().Str("backend", name).Err(err).
								Msg("[upstream-cluster] promote from sick skipped (state changed)")
						}
						delete(successes, name) // сбросить счётчик
					}
				} else {
					// Любая ошибка — сброс последовательности
					if successes[name] != 0 {
						successes[name] = 0
					}
				}
			}
		}
	}
}

// runDeadMonitor: attempts to resurrect with exp backoff + jitter.
func (c *Cluster) runDeadMonitor(ctx context.Context) {
	cfg := withDefaults()
	backoff := func(attempt int) time.Duration {
		b := cfg.DeadRetryBackoff * time.Duration(1<<uint(min(attempt, 8)))
		return jitter(b, cfg.JitterFrac)
	}

	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff(attempt)):
			attempt++
			for name, s := range c.all {
				if State(s.state.Load()) != Dead {
					continue
				}
				if s.be.IsHealthy() == nil {
					_ = c.Promote(name)
					attempt = 0
				}
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
