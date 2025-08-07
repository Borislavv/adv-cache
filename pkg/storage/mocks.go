package storage

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/mock"
	"github.com/rs/zerolog/log"
)

func LoadMocks(ctx context.Context, config config.Config, storage Storage, num int) {
	go func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()

		log.Info().Msg("[mocks] mock data start loading")
		defer log.Info().Msg("[mocks] mocked data finished loading")

		path := []byte("/api/v2/pagedata")
		for entry := range mock.StreamEntryPointersConsecutive(ctx, config, path, num) {
			storage.Set(entry)
		}
	}()
}
