package middleware

import (
	"context"
	"github.com/rs/zerolog/log"
)

func (middleware *TraefikCacheMiddleware) run(ctx context.Context) error {
	log.Info().Msg("[advanced-cache] starting")

	middleware.ctx = ctx

	cfg, err := middleware.loadConfig()
	if err != nil {
		log.Error().Err(err).Msg("[advanced-cache] failed to load config")
		return err
	}
	middleware.cfg = cfg

	middleware.setUpCache()

	if err := middleware.loadDump(); err != nil {
		log.Error().Err(err).Msg("[dump] failed to load")
	}

	middleware.store.Run()
	middleware.evictor.Run()
	middleware.refresher.Run()
	middleware.runControllerLogger()

	log.Info().Msg("[advanced-cache] has been started")

	return nil
}
