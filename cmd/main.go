package main

import (
	"context"
	"github.com/Borislavv/advanced-cache/internal/cache"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/gc"
	"github.com/Borislavv/advanced-cache/pkg/k8s/probe/liveness"
	"github.com/Borislavv/advanced-cache/pkg/shutdown"
	"github.com/rs/zerolog/log"
	"go.uber.org/automaxprocs/maxprocs"
	"runtime"
	"time"
)

const (
	configPath      = "advancedCache.cfg.yaml"
	configPathLocal = "advancedCache.cfg.local.yaml"
)

// setMaxProcs automatically sets the optimal GOMAXPROCS value (CPU parallelism)
// based on the available CPUs and cgroup/docker CPU quotas (uses automaxprocs).
func setMaxProcs() {
	if _, err := maxprocs.Set(); err != nil {
		log.Err(err).Msg("[main] setting up GOMAXPROCS value failed")
		panic(err)
	}
	log.Info().Msgf("[main] optimized GOMAXPROCS=%d was set up", runtime.GOMAXPROCS(0))
}

// loadCfg loads the configuration struct from environment variables
// and computes any derived configuration values.
func loadCfg() (*config.Cache, error) {
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

// Main entrypoint: configures and starts the cache application.
func main() {
	// Create a root context for gracefulShutdown shutdown and cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Optimize GOMAXPROCS for the current environment.
	setMaxProcs()

	// Load the application configuration from env vars.
	cfg, cfgError := loadCfg()
	if cfgError != nil {
		log.Err(cfgError).Msg("[main] failed to load cache config")
		return
	}

	// Setup gracefulShutdown shutdown handler (SIGTERM, SIGINT, etc).
	gracefulShutdown := shutdown.NewGraceful(ctx, cancel)
	gracefulShutdown.SetGracefulTimeout(time.Minute * 5)

	// Initialize liveness probe for Kubernetes/Cloud health checks.
	probe := liveness.NewProbe(cfg.Cache.K8S.Probe.Timeout)

	// Initialize and start the cache application.
	app, err := cache.NewApp(ctx, cfg, probe)
	if err != nil {
		log.Err(err).Msg("[main] failed to init cache app")
	}

	// Register app for gracefulShutdown shutdown.
	gracefulShutdown.Add(1)
	go app.Start(gracefulShutdown)

	gcCtx, gcCancel := context.WithCancel(context.Background())
	defer gcCancel()

	// Run forced GC.
	go gc.Run(gcCtx, cfg)

	// Listen for OS signals or context cancellation and wait for gracefulShutdown shutdown.
	if err := gracefulShutdown.ListenCancelAndAwait(); err != nil {
		log.Err(err).Msg("failed to gracefully shut down service")
	}
}
