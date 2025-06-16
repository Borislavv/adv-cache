package main

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/internal/cache"
	"github.com/Borislavv/traefik-http-cache-plugin/internal/cache/config"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/k8s/probe/liveness"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/shutdown"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"go.uber.org/automaxprocs/maxprocs"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"time"
)
import _ "net/http/pprof"

// Initializes environment variables from .env files and binds them using Viper.
// This allows overriding any value via environment variables.
func init() {
	// Load .env and .env.local files for configuration overrides.
	if err := godotenv.Overload(".env", ".env.local"); err != nil {
		panic(err)
	}

	// Bind all relevant environment variables using Viper.
	viper.AutomaticEnv()
	_ = viper.BindEnv("APP_ENV")
	_ = viper.BindEnv("APP_DEBUG")
	_ = viper.BindEnv("INIT_STORAGE_LEN_PER_SHARD")
	_ = viper.BindEnv("EVICTION_ALGO")
	_ = viper.BindEnv("MEMORY_FILL_THRESHOLD")
	_ = viper.BindEnv("MEMORY_LIMIT")
	_ = viper.BindEnv("REVALIDATE_BETA")
	_ = viper.BindEnv("REVALIDATE_INTERVAL")
	_ = viper.BindEnv("BACKEND_URL")
	_ = viper.BindEnv("FASTHTTP_SERVER_NAME")
	_ = viper.BindEnv("FASTHTTP_SERVER_PORT")
	_ = viper.BindEnv("FASTHTTP_SERVER_SHUTDOWN_TIMEOUT")
	_ = viper.BindEnv("FASTHTTP_SERVER_REQUEST_TIMEOUT")
	_ = viper.BindEnv("LIVENESS_PROBE_FAILED_TIMEOUT")
	_ = viper.BindEnv("IS_PROMETHEUS_METRICS_ENABLED")
}

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
func loadCfg() *config.Config {
	cfg := &config.Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		log.Err(err).Msg("[main] failed to unmarshal config from envs")
		panic(err)
	}
	// Calculate the refresh duration threshold as a function of revalidate interval and beta.
	cfg.RefreshDurationThreshold = time.Duration(float64(cfg.RevalidateInterval) * cfg.RevalidateBeta)
	return cfg
}

// Main entrypoint: configures and starts the cache application.
func main() {
	go func() {
		http.ListenAndServe("localhost:6060", nil)
	}()

	f, err := os.Create("cpu_profile.pprof")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	// Запускаем запись CPU профиля
	if err = pprof.StartCPUProfile(f); err != nil {
		panic(err)
	}
	// Обязательно останови профиль перед завершением программы
	defer pprof.StopCPUProfile()

	// Create a root context for gracefulShutdown shutdown and cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Optimize GOMAXPROCS for the current environment.
	setMaxProcs()

	// Load the application configuration from env vars.
	cfg := loadCfg()

	// Setup gracefulShutdown shutdown handler (SIGTERM, SIGINT, etc).
	gracefulShutdown := shutdown.NewGraceful(ctx, cancel)
	gracefulShutdown.SetGracefulTimeout(time.Millisecond * 9500) // 9.5s

	// Initialize liveness probe for Kubernetes/Cloud health checks.
	probe := liveness.NewProbe(cfg.LivenessProbeTimeout)

	// Initialize and start the cache application.
	if app, err := cache.NewApp(ctx, cfg, probe); err != nil {
		log.Err(err).Msg("[main] failed to init cache app")
	} else {
		// Register app for gracefulShutdown shutdown.
		gracefulShutdown.Add(1)
		go app.Start(gracefulShutdown)
	}

	// Listen for OS signals or context cancellation and wait for gracefulShutdown shutdown.
	if err := gracefulShutdown.ListenCancelAndAwait(); err != nil {
		log.Err(err).Msg("failed to gracefully shut down service")
	}
}
