package advancedcachemiddleware

import (
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/rs/zerolog/log"
)

var Dumper storage.Dumper

func (m *AdvancedCacheMiddleware) setUpCache() {
	enabled.Store(m.cfg.Cache.Enabled)

	m.metrics = metrics.New()
	m.backend = repository.NewBackend(m.ctx, m.cfg)
	m.storage = storage.NewStorage(m.ctx, m.cfg, m.backend)

	Dumper = storage.NewDumper(m.cfg, m.storage, m.backend)
	if m.cfg.Cache.Persistence.Dump.IsEnabled {
		if err := Dumper.Load(m.ctx); err != nil {
			log.Error().Err(err).Msg("[dump] failed to load cache dump")
		}
	}

	if m.cfg.Cache.Persistence.Mock.Enabled {
		storage.LoadMocks(m.ctx, m.cfg, m.backend, m.storage, m.cfg.Cache.Persistence.Mock.Length)
	}
}
