package liveness

import (
	"github.com/rs/zerolog/log"
	"time"
)

type Config struct {
	// Timeout is a name of the shared server.
	Timeout string `envconfig:"LIVENESS_PROBE_TIMEOUT" mapstructure:"LIVENESS_PROBE_TIMEOUT" default:"5s"`
}

func (c Config) LivenessTimeout() time.Duration {
	d, err := time.ParseDuration(c.Timeout)
	if err != nil {
		log.Warn().Msgf("Failed to parse liveness probe timeout: %v, was set a default: 5s", err)
		return time.Second * 5
	}
	return d
}
