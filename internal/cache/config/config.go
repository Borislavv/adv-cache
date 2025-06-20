package config

import (
	cacheConfig "github.com/Borislavv/traefik-http-cache-plugin/pkg/config"
	prometheusconifg "github.com/Borislavv/traefik-http-cache-plugin/pkg/prometheus/metrics/config"
	fasthttpconfig "github.com/Borislavv/traefik-http-cache-plugin/pkg/server/config"
)

type Config struct {
	*cacheConfig.V2          // loads from yaml
	cacheConfig.Cache        `mapstructure:",squash"`
	prometheusconifg.Metrics `mapstructure:",squash"`
	fasthttpconfig.Server    `mapstructure:",squash"`
}
