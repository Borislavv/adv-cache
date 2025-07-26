package advancedcachemiddleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/gc"
	"github.com/rs/zerolog/log"
)

func (m *AdvancedCacheMiddleware) run(ctx context.Context, config *TraefikIntermediateConfig) error {
	log.Info().Msg("[advanced-cache] starting")

	m.ctx = ctx

	if cfg, err := m.loadConfig(config); err != nil {
		log.Error().Err(err).Msg("[advanced-cache] failed to config")
		return err
	} else {
		m.cfg = cfg
	}

	m.setUpCache()
	m.runLoggerMetricsWriter()
	gc.Run(ctx, m.cfg)

	log.Info().Msg("[advanced-cache] has been started")

	return nil
}
