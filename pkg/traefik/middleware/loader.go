package middleware

import (
	"context"
	"github.com/rs/zerolog/log"
	"time"
)

func (m *TraefikCacheMiddleware) loadDump() error {
	if err := m.dumper.Load(m.ctx); err != nil {
		return err
	}

	go func() {
		<-m.ctx.Done()

		dCtx, dCancel := context.WithTimeout(context.Background(), 9*time.Second)
		defer dCancel()

		if err := m.dumper.Dump(dCtx); err != nil {
			log.Error().Err(err).Msg("[dump] failed to store dump")
		}
	}()

	return nil
}
