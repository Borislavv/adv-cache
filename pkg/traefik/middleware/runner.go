package middleware

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/gc"
	"github.com/rs/zerolog/log"
)

func (m *TraefikCacheMiddleware) run(ctx context.Context, config *config.TraefikIntermediateConfig) error {
	log.Info().Msg("[advanced-cache] starting")

	m.ctx = ctx
	cfg, err := m.loadConfig(config)
	if err != nil {
		log.Error().Err(err).Msg("[advanced-cache] failed to load config")
		return err
	}
	m.cfg = cfg

	m.setUpCache()

	gc.Run(ctx, cfg)

	log.Info().Msg("[advanced-cache] has been started")

	return nil
}
