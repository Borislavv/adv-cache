package cache

import (
	"context"
	"github.com/Borislavv/advanced-cache/internal/cache/server"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/k8s/probe/liveness"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/shutdown"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	"github.com/rs/zerolog/log"
	"time"
)

// App defines the cache application lifecycle interface.
type App interface {
	Start()
}

// Cache encapsulates the entire cache application state, including HTTP server, config, and probes.
type Cache struct {
	cfg     *config.Cache
	ctx     context.Context
	cancel  context.CancelFunc
	probe   liveness.Prober
	dumper  storage.Dumper
	server  server.Http
	backend repository.Backender
	db      storage.Storage
}

// NewApp builds a new Cache app, wiring together db, repo, reader, and server.
func NewApp(ctx context.Context, cfg *config.Cache, probe liveness.Prober) (*Cache, error) {
	ctx, cancel := context.WithCancel(ctx)

	meter := metrics.New()
	backend := repository.NewBackend(ctx, cfg)
	db := lru.NewStorage(ctx, cfg, backend)
	dumper := storage.NewDumper(cfg, db, backend)

	cacheObj := &Cache{
		ctx:     ctx,
		cancel:  cancel,
		cfg:     cfg,
		db:      db,
		backend: backend,
		probe:   probe,
		dumper:  dumper,
	}

	// Compose the HTTP server (API, metrics and so on)
	srv, err := server.New(ctx, cfg, db, backend, probe, meter)
	if err != nil {
		cancel()
		return nil, err
	}
	cacheObj.server = srv

	return cacheObj, nil
}

// Start runs the cache server and liveness probe, and handles graceful shutdown.
// The Gracefuller interface is expected to call Done() when shutdown is complete.
func (c *Cache) Start(gc shutdown.Gracefuller) {
	defer func() {
		c.stop()
		gc.Done()
	}()

	log.Info().Msg("[app] starting cache")

	if c.cfg.Cache.Enabled {
		if c.cfg.Cache.Persistence.Dump.IsEnabled {
			if err := c.dumper.Load(c.ctx); err != nil {
				log.Warn().Msg("[dump] failed to load dump: " + err.Error())
			}
		}
		if c.cfg.Cache.Persistence.Mock.Enabled {
			storage.LoadMocks(c.ctx, c.cfg, c.backend, c.db, c.cfg.Cache.Persistence.Mock.Length)
		}
	}

	waitCh := make(chan struct{})

	go func() {
		defer close(waitCh)
		c.probe.Watch(c) // Call first due to it does not block the green-thread
		c.server.Start() // Blocks the green-thread until the server will be stopped
	}()

	log.Info().Msg("[app] cache has been started")

	<-waitCh // Wait until the server exits
}

// stop cancels the main application context and logs shutdown.
func (c *Cache) stop() {
	log.Info().Msg("[app] stopping cache")

	defer c.cancel()

	// spawn a new one with limit for k8s timeout before the service will be received SIGKILL
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := c.dumper.Dump(ctx); err != nil {
		log.Err(err).Msg("[dump] failed to store cache dump")
	}

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
