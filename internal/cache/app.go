package cache

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/upstream/cluster"
	"time"

	"github.com/Borislavv/advanced-cache/internal/cache/server"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/k8s/probe/liveness"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/shutdown"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	"github.com/rs/zerolog/log"
)

const (
	ConfigPath      = "advancedCache.cfg.yaml"
	ConfigPathLocal = "advancedCache.cfg.local.yaml"
)

type Version struct {
	Name    string
	Size    int64
	ModTime time.Time
}

// App defines the cache application lifecycle interface.
type App interface {
	Start(gc shutdown.Gracefuller)
	loadDataInteractive(ctx context.Context) error
}

// Cache encapsulates the entire cache application state.
type Cache struct {
	cfg           config.Config
	ctx           context.Context
	cancel        context.CancelFunc
	cluster       cluster.Upstream
	db            storage.Storage
	probe         liveness.Prober
	server        server.Http
	isInteractive bool
}

// NewApp builds a new Cache app.
func NewApp(ctx context.Context, cfg config.Config, probe liveness.Prober, isInteractive bool) (cache *Cache, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	var upstream cluster.Upstream
	if upstream, err = cluster.New(ctx, cfg); err != nil {
		log.Error().Err(err).Msg("[app] failed to make a new upstream cluster")
		return nil, err
	}

	db := lru.NewStorage(ctx, cfg, upstream)
	srv, err := server.New(ctx, cfg, db, upstream, probe, metrics.New())
	if err != nil {
		cancel()
		return nil, err
	}

	return &Cache{
		ctx:           ctx,
		cancel:        cancel,
		cfg:           cfg,
		probe:         probe,
		db:            db,
		server:        srv,
		cluster:       upstream,
		isInteractive: isInteractive,
	}, nil
}

// Start runs the cache server and probes, handles shutdown.
func (c *Cache) Start(gsh shutdown.Gracefuller) error {
	log.Info().Msg("[app] starting")

	// load dump/mocks if necessary
	if err := c.loadData(c.ctx, c.isInteractive); err != nil {
		log.Err(err).Msg("[main] failed to load data")
		return err
	}

	go func() {
		defer func() {
			c.stop()
			gsh.Done()
		}()
		c.db.Run()
		c.probe.Watch(c)
		c.server.Start()
	}()

	log.Info().Msg("[app] has been started")

	return nil
}

// stop cancels context and dumps cache on shutdown.
func (c *Cache) stop() {
	log.Info().Msg("[app] stopping")
	defer c.cancel()

	// store dump if necessary
	if err := c.db.Close(); err != nil {
		log.Error().Err(err).Msg("[app] closing db error")
	}

	log.Info().Msg("[app] has been stopped")
}

func (c *Cache) IsAlive(_ context.Context) bool {
	if !c.server.IsAlive() {
		log.Info().Msg("[app] http server has gone away")
		return false
	}
	return true
}
