package cache

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/internal/cache/config"
	"github.com/Borislavv/traefik-http-cache-plugin/internal/cache/server"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/k8s/probe/liveness"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/repository"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/shutdown"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/cache/lru"
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
	"github.com/rs/zerolog/log"
)

// App defines the cache application lifecycle interface.
type App interface {
	Start()
}

// Cache encapsulates the entire cache application state, including HTTP server, config, and probes.
type Cache struct {
	cfg    *config.Config     // Application configuration
	ctx    context.Context    // Application context for cancellation and shutdown
	cancel context.CancelFunc // Cancel function for ctx
	probe  liveness.Prober    // Liveness probe integration
	server server.Http        // HTTP server (implements business logic and API)
}

// NewApp builds a new Cache app, wiring together storage, repo, reader, and server.
func NewApp(ctx context.Context, cfg *config.Config, probe liveness.Prober) (*Cache, error) {
	ctx, cancel := context.WithCancel(ctx)

	// Setup sharded map for high-concurrency cache storage.
	shardedMap := sharded.NewMap[*model.Response](cfg.InitStorageLengthPerShard)
	backend := repository.NewBackend(cfg.V2)
	balancer := lru.NewBalancer(ctx, shardedMap)
	refresher := lru.NewRefresher(ctx, &cfg.Cache, balancer)
	db := storage.New(ctx, cfg.V2, balancer, refresher, backend, shardedMap)

	// Compose the HTTP server (API, metrics and so on)
	srv, err := server.New(ctx, cfg, db, backend, probe)
	if err != nil {
		cancel()
		return nil, err
	}

	return &Cache{
		ctx:    ctx,
		cancel: cancel,
		cfg:    cfg,
		probe:  probe,
		server: srv,
	}, nil
}

// Start runs the cache server and liveness probe, and handles graceful shutdown.
// The Gracefuller interface is expected to call Done() when shutdown is complete.
func (c *Cache) Start(gc shutdown.Gracefuller) {
	defer func() {
		c.stop()
		gc.Done()
	}()

	log.Info().Msg("[app] starting cache")

	waitCh := make(chan struct{})

	go func() {
		defer close(waitCh)
		c.probe.Watch(c) // Call first due to it does not block the green-thread
		c.server.Start() // Blocks the green-thread
	}()

	log.Info().Msg("[app] cache has been started")

	<-waitCh // Wait until the server exits
}

// stop cancels the main application context and logs shutdown.
func (c *Cache) stop() {
	log.Info().Msg("[app] stopping cache")
	c.cancel()
	log.Info().Msg("[app] cache has been stopped")
}

// IsAlive is called by liveness probes to check app health.
// Returns false if the HTTP server is not alive.
func (c *Cache) IsAlive(_ context.Context) bool {
	if !c.server.IsAlive() {
		log.Info().Msg("[app] http server has gone away")
		return false
	}
	return true
}
