package advancedcache

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/gc"
	"github.com/rs/zerolog/log"
)

func (m *CacheMiddleware) run(ctx context.Context) error {
	log.Info().Msg("[advanced-cache] starting")

	m.ctx = ctx

	if err := m.loadConfig(); err != nil {
		return err
	}

	m.setUpCache()
	m.runLoggerMetricsWriter()
	gc.Run(ctx, m.cfg)

	log.Info().Msg("[advanced-cache] has been started")

	return nil
}
