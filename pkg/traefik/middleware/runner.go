package middleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/gc"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/rs/zerolog/log"
)

func (m *TraefikCacheMiddleware) run(ctx context.Context) error {
	log.Info().Msg("[advanced-cache] starting")

	m.ctx = ctx

	cfg, err := m.loadConfig()
	if err != nil {
		log.Error().Err(err).Msg("[advanced-cache] failed to load config")
		return err
	}
	m.cfg = cfg

	m.setUpCache()
	gc.Run(ctx, cfg)
	m.runLoggerMetricsWriter()

	if cfg.Cache.Enabled {
		if cfg.Cache.Persistence.Dump.IsEnabled {
			if err = m.dumper.Load(m.ctx); err != nil {
				log.Error().Err(err).Msg("[dump] failed to load")
			}
		}
		if cfg.Cache.Persistence.Mock.Enabled {
			storage.LoadMocks(m.ctx, m.cfg, m.backend, m.storage, m.cfg.Cache.Persistence.Mock.Length)
		}
	}

	log.Info().Msg("[advanced-cache] has been started")

	return nil
}
