package advancedcache

import (
	"context"
	"github.com/rs/zerolog/log"
)

func (m *CacheMiddleware) run(ctx context.Context) error {
	log.Info().Msg("[advanced-cache] starting")

	m.ctx = ctx

	if err := m.loadConfig(); err != nil {
		return err
	}

	enabled.Store(m.cfg.Cache.Enabled)

	m.setUpCache()

	if err := m.loadDump(); err != nil {
		log.Error().Err(err).Msg("[dump] failed to load")
	}

	m.storage.Run()
	m.evictor.Run()
	m.refresher.Run()
	m.runLoggerMetricsWriter()

	log.Info().Msg("[advanced-cache] has been started")

	return nil
}
