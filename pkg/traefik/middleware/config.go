package middleware

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/rs/zerolog/log"
)

const (
	configPath      = "advancedCache.cfg.yaml"
	configPathLocal = "advancedCache.cfg.local.yaml"
)

func (m *TraefikCacheMiddleware) loadConfig() (*config.Cache, error) {
	cfg, err := config.LoadConfig(configPathLocal)
	if err != nil {
		cfg, err = config.LoadConfig(configPath)
		if err != nil {
			log.Err(err).Msg("[config] failed to load")
			return nil, err
		} else {
			log.Info().Msgf("[config] config loaded from '%v'", configPath)
		}
	} else {
		log.Info().Msgf("[config] config loaded from '%v'", configPathLocal)
	}
	return cfg, nil
}
