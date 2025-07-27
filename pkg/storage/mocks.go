package storage

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/mock"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/rs/zerolog/log"
)

func LoadMocks(ctx context.Context, config *config.Cache, backend repository.Backender, storage Storage, num int) {
	go func() {
		log.Info().Msg("[dump] dump restored 0 keys, mock data start loading")
		defer log.Info().Msg("[dump] mocked data finished loading")
		path := []byte("/api/v2/pagedata")
		for entry := range mock.StreamEntryPointersConsecutive(ctx, config, backend, path, num) {
			if inserted, persisted := storage.Set(entry); persisted {
				inserted.Release()
			} else {
				inserted.Remove()
			}
		}
	}()
}
