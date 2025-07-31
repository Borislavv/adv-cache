package middleware

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
)

func (m *TraefikCacheMiddleware) loadConfig(cfg *config.TraefikIntermediateConfig) (*config.Cache, error) {
	return config.LoadConfig(cfg.ConfigPath)
}
