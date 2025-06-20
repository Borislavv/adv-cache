package config

import (
	cacheConfig "github.com/Borislavv/traefik-http-cache-plugin/pkg/config"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/k8s/probe/liveness"
	prometheusconifg "github.com/Borislavv/traefik-http-cache-plugin/pkg/prometheus/metrics/config"
	fasthttpconfig "github.com/Borislavv/traefik-http-cache-plugin/pkg/server/config"
)

type Config struct {
	*cacheConfig.Cache       // loads from yaml
	prometheusconifg.Metrics `mapstructure:",squash"`
	fasthttpconfig.Server    `mapstructure:",squash"`
	liveness.Config          `mapstructure:",squash"`
}
