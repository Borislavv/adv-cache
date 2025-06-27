package advancedcache

import (
	"context"
	"github.com/rs/zerolog/log"
)

func (middleware *CacheMiddleware) run(ctx context.Context) error {
	log.Info().Msg("[advanced-cache] starting")

	middleware.ctx = ctx

	if err := middleware.loadConfig(); err != nil {
		return err
	}

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
