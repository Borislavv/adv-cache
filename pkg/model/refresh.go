package model

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/ctime"
	"math"
	"math/rand/v2"
	"sync/atomic"
)

func (e *Entry) UpdatedAt() int64 { return atomic.LoadInt64(&e.updatedAt) }
func (e *Entry) touch()           { atomic.StoreInt64(&e.updatedAt, ctime.UnixNano()) }

// ShouldBeRefreshed implements probabilistic refresh logic ("beta" algorithm).
// Returns true if the entry is stale and, with a probability proportional to its staleness, should be refreshed now.
func (e *Entry) ShouldBeRefreshed(cfg config.Config) bool {
	if e == nil {
		return false
	}

	var (
		ttl         = cfg.Refresh().TTL.Nanoseconds()
		beta        = cfg.Refresh().Beta
		coefficient = cfg.Refresh().Coefficient
	)

	if e.rule.Load().Refresh != nil {
		if !e.rule.Load().Refresh.Enabled {
			return false
		}

		if e.rule.Load().Refresh.TTL.Nanoseconds() > 0 {
			ttl = e.rule.Load().Refresh.TTL.Nanoseconds()
		}
		if e.rule.Load().Refresh.Beta > 0 {
			beta = e.rule.Load().Refresh.Beta
		}
		if e.rule.Load().Refresh.Coefficient > 0 {
			coefficient = e.rule.Load().Refresh.Coefficient
		}
	}

	// время, прошедшее с последнего обновления
	elapsed := ctime.UnixNano() - atomic.LoadInt64(&e.updatedAt)
	minStale := int64(float64(ttl) * coefficient)

	if minStale > elapsed {
		return false
	}

	// нормируем x = elapsed / ttl в [0,1]
	x := float64(elapsed) / float64(ttl)
	if x < 0 {
		x = 0
	} else if x > 1 {
		x = 1
	}

	// вероятность экспоненциального распределения
	prob := 1 - math.Exp(-beta*x)
	return rand.Float64() < prob
}
