package middleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/gc"
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

	if err = m.loadDump(); err != nil {
		log.Error().Err(err).Msg("[dump] failed to load")
	}

	m.storage.Run()
	m.evictor.run()
	m.refresher.run()
	m.runLoggerMetricsWriter()
	gc.Run(ctx, cfg)

	log.Info().Msg("[advanced-cache] has been started")

	return nil
}
