package main

import (
	"context"
	"flag"
	"github.com/Borislavv/advanced-cache/internal/cache"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/gc"
	"github.com/Borislavv/advanced-cache/pkg/k8s/probe/liveness"
	"github.com/Borislavv/advanced-cache/pkg/shutdown"
	//	"github.com/Borislavv/advanced-cache/pkg/tui"
	"github.com/rs/zerolog/log"
	"go.uber.org/automaxprocs/maxprocs"
	"runtime"
	"time"
)

var (
	customCfg     = flag.String("cfg", "/path/to/config", "custom config file")
	isInteractive = flag.Bool("inter", false, "Enable interactive mode of data loading")
)

func init() {
	flag.Parse()
}

// setMaxProcs automatically sets the optimal GOMAXPROCS value (CPU parallelism)
// based on the available CPUs and cgroup/docker CPU quotas (uses automaxprocs).
func setMaxProcs(cfg config.Config) {
	if cfg.Runtime().Gomaxprocs == 0 {
		if _, err := maxprocs.Set(); err != nil {
			log.Err(err).Msg("[main] setting up GOMAXPROCS value failed")
		}
	} else {
		runtime.GOMAXPROCS(cfg.Runtime().Gomaxprocs)
	}
	log.Info().Msgf("[main] GOMAXPROCS=%d was set up", runtime.GOMAXPROCS(0))
}

// loadCfg loads the configuration struct from environment variables
// and computes any derived configuration values.
func loadCfg(path string) (config.Config, error) {
	if path != "" {
		cfg, err := config.LoadConfig(path)
		if err != nil {
			log.Error().Err(err).Msgf("[config] failed to load custom config from '%v'", path)
			return nil, err
		} else {
			log.Info().Msgf("[config] custom config loaded from '%v'", path)
			return cfg, nil
		}
	}

	cfg, err := config.LoadConfig(cache.ConfigPathLocal)
	if err != nil {
		cfg, err = config.LoadConfig(cache.ConfigPath)
		if err != nil {
			log.Err(err).Msg("[config] failed to load")
			return nil, err
		} else {
			log.Info().Msgf("[config] config loaded from '%v'", cache.ConfigPath)
		}
	} else {
		log.Info().Msgf("[config] config loaded from '%v'", cache.ConfigPathLocal)
	}
	return cfg, nil
}

// Main entrypoint: configures and starts the cache application.
func main() {
	// Create a root context for gracefulShutdown shutdown and cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load the application configuration from env vars.
	cfg, err := loadCfg(*customCfg)
	if err != nil {
		log.Err(err).Msg("[main] failed to load cache config")
		return
	}

	// Optimize GOMAXPROCS for the current environment.
	setMaxProcs(cfg)

	// Setup gracefulShutdown shutdown handler (SIGTERM, SIGINT, etc).
	gsh := shutdown.NewGraceful(ctx, cancel)
	gsh.SetGracefulTimeout(time.Minute * 3)

	// Initialize liveness probe for Kubernetes/Cloud health checks.
	probe := liveness.NewProbe(cfg.K8S().Probe.Timeout)

	// Initialize and start the cache application.
	app, err := cache.NewApp(ctx, cfg, probe, *isInteractive)
	if err != nil {
		log.Err(err).Msg("[main] failed to init. cache app")
		return
	}

	// Register app for gracefulShutdown shutdown.
	gsh.Add(1)
	if err = app.Start(gsh); err != nil {
		log.Err(err).Msg("[main] failed to start app")
		return
	} else {
		// Run forced GC.
		gc.Run(ctx, cfg)
	}

	// Listen for OS signals or context cancellation and wait for gracefulShutdown shutdown.
	if err = gsh.ListenCancelAndAwait(); err != nil {
		log.Err(err).Msg("[main] failed to gracefully shut down service")
		return
	}
}
