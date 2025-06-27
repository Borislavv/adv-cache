package advancedcache

import (
	"context"
	"github.com/rs/zerolog/log"
	"time"
)

func (middleware *CacheMiddleware) loadDump() error {
	if err := middleware.dumper.Load(middleware.ctx); err != nil {
		return err
	}

	go func() {
		<-middleware.ctx.Done()

		dCtx, dCancel := context.WithTimeout(context.Background(), 9*time.Second)
		defer dCancel()

		if err := middleware.dumper.Dump(dCtx); err != nil {
			log.Error().Err(err).Msg("[dump] failed to store dump")
		}
	}()

	return nil
}
