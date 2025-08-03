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
	from          = flag.String("from", "http://localhost:8080", "Origin server address to proxy requests from")
	to            = flag.String("to", ":8020", "Port or address to serve the cache application on")
	mocks         = flag.Bool("mocks", false, "Enable mocks mode (for dev/testing)")
	mocksLen      = flag.Int("mockslen", 10000, "Length of mock data to generate if mocks mode is enabled")
	dump          = flag.Bool("dump", false, "Enable dump loading at startup and writing at shutdown")
	refresh       = flag.Bool("refresh", false, "Enable background data refresh")
	eviction      = flag.Bool("eviction", false, "Enable data eviction on overflow")
	upstreamRate  = flag.Int("upstreamrate", 1000, "Maximum rate of upstream requests per second")
	memoryLimit   = flag.Int("memorylimit", 34359738368, "Maximum amount of bytes that can be used to cache evictions")
	gomaxprocs    = flag.Int("procs", 3, "Maximum number of CPU cores to use")
	isInteractive = flag.Bool("inter", false, "Enable interactive mode of data loading")

	fromDefined         = false
	toDefined           = false
	mocksDefined        = false
	mockslenDefined     = false
	dumpDefined         = false
	refreshDefined      = false
	evictionDefined     = false
	upstreamRateDefined = false
	memoryLimitDefined  = false
	gomaxprocsDefined   = false
)

func init() {
	flag.Parse()

	logEvent := log.Info()
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "from" {
			fromDefined = true
			logEvent.Str("from", *from)
		} else if f.Name == "to" {
			toDefined = true
			logEvent.Str("to", *to)
		} else if f.Name == "mocks" {
			mocksDefined = true
			logEvent.Bool("mocks", *mocks)
		} else if f.Name == "mockslen" {
			mockslenDefined = true
			logEvent.Int("mockslen", *mocksLen)
		} else if f.Name == "dump" {
			dumpDefined = true
			logEvent.Bool("dump", *dump)
		} else if f.Name == "refresh" {
			refreshDefined = true
			logEvent.Bool("refresh", *refresh)
		} else if f.Name == "eviction" {
			evictionDefined = true
			logEvent.Bool("eviction", *eviction)
		} else if f.Name == "upstreamrate" {
			upstreamRateDefined = true
			logEvent.Int("upstreamrate", *upstreamRate)
		} else if f.Name == "memorylimit" {
			memoryLimitDefined = true
			logEvent.Int("memorylimit", *memoryLimit)
		} else if f.Name == "procs" {
			gomaxprocsDefined = true
			logEvent.Int("procs", *gomaxprocs)
		}
	})

	logEvent.Msg("[main] startup flags")
}

// setMaxProcs automatically sets the optimal GOMAXPROCS value (CPU parallelism)
// based on the available CPUs and cgroup/docker CPU quotas (uses automaxprocs).
func setMaxProcs(cfg *config.Cache) {
	if cfg.Cache.Runtime.Gomaxprocs == 0 {
		if _, err := maxprocs.Set(); err != nil {
			log.Err(err).Msg("[main] setting up GOMAXPROCS value failed")
		}
	} else {
		runtime.GOMAXPROCS(cfg.Cache.Runtime.Gomaxprocs)
	}
	log.Info().Msgf("[main] GOMAXPROCS=%d was set up", runtime.GOMAXPROCS(0))
}

// loadCfg loads the configuration struct from environment variables
// and computes any derived configuration values.
func loadCfg() (*config.Cache, error) {
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

	if fromDefined {
		cfg.Cache.Proxy.From = *from
	}
	if toDefined {
		cfg.Cache.Proxy.To = *to
	}
	if mocksDefined {
		cfg.Cache.Persistence.Mock.Enabled = *mocks
	}
	if mockslenDefined {
		cfg.Cache.Persistence.Mock.Length = *mocksLen
	}
	if dumpDefined {
		cfg.Cache.Persistence.Dump.IsEnabled = *dump
	}
	if refreshDefined {
		cfg.Cache.Refresh.Enabled = *dump
	}
	if evictionDefined {
		cfg.Cache.Eviction.Enabled = *eviction
	}
	if upstreamRateDefined {
		cfg.Cache.Proxy.Rate = *upstreamRate
	}
	if memoryLimitDefined {
		cfg.Cache.Storage.Size = uint(*memoryLimit)
	}
	if gomaxprocsDefined {
		cfg.Cache.Runtime.Gomaxprocs = *gomaxprocs
	}

	return cfg, nil
}

// Main entrypoint: configures and starts the cache application.
func main() {
	// Create a root context for gracefulShutdown shutdown and cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load the application configuration from env vars.
	cfg, err := loadCfg()
	if err != nil {
		log.Err(err).Msg("[main] failed to load cache config")
		return
	}

	// Optimize GOMAXPROCS for the current environment.
	setMaxProcs(cfg)

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

	// Load data manually or interactive, as you wish
	if err = app.LoadData(ctx, *isInteractive); err != nil {
		log.Err(err).Msg("[main] failed to load data")
		return
	}

	// Register app for gracefulShutdown shutdown.
	gracefulShutdown.Add(1)
	go app.Start(gracefulShutdown)

	gcCtx, gcCancel := context.WithCancel(context.Background())
	defer gcCancel()

	// Run forced GC.
	go gc.Run(gcCtx, cfg)

	// Listen for OS signals or context cancellation and wait for gracefulShutdown shutdown.
	if err = gracefulShutdown.ListenCancelAndAwait(); err != nil {
		log.Err(err).Msg("failed to gracefully shut down service")
	}
}
