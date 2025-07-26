package advancedcachemiddleware

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
)

func (m *AdvancedCacheMiddleware) loadConfig(cfg *TraefikIntermediateConfig) (*config.Cache, error) {
	return config.LoadConfig(cfg.ConfigPath)
}
