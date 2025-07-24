package advancedcache

import (
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
)

func (m *CacheMiddleware) setUpCache() {
	m.metrics = metrics.New()
	m.backend = repository.NewBackend(m.ctx, m.cfg)
	m.storage = lru.NewStorage(m.ctx, m.cfg, m.backend)
}
