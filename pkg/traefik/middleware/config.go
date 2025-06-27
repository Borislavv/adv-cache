package middleware

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

func (middleware *TraefikCacheMiddleware) loadConfig() (*config.Cache, error) {
	type configPath struct {
		ConfigPath string `envconfig:"CACHE_CONFIG_PATH" mapstructure:"CACHE_CONFIG_PATH" default:"/config/config.dev.yaml"`
	}

	cfgPath := &configPath{}
	if err := viper.Unmarshal(cfgPath); err != nil {
		log.Err(err).Msg("[main] failed to unmarshal config from envs")
		return nil, err
	}

	cacheConfig, err := config.LoadConfig(cfgPath.ConfigPath)
	if err != nil {
		log.Err(err).Msg("[main] failed to load config from envs")
		return nil, err
	}

	return cacheConfig, nil
}
