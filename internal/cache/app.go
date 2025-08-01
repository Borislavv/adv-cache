package cache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Borislavv/advanced-cache/internal/cache/server"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/k8s/probe/liveness"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	"github.com/Borislavv/advanced-cache/pkg/shutdown"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/Borislavv/advanced-cache/pkg/storage/lru"
	"github.com/manifoldco/promptui"
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
	cfg     *config.Cache
	ctx     context.Context
	cancel  context.CancelFunc
	probe   liveness.Prober
	dumper  storage.Dumper
	server  server.Http
	backend repository.Backender
	db      storage.Storage
}

// NewApp builds a new Cache app.
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

	srv, err := server.New(ctx, cfg, db, backend, probe, meter)
	if err != nil {
		cancel()
		return nil, err
	}
	cacheObj.server = srv

	return cacheObj, nil
}

// Start runs the cache server and probes, handles shutdown.
func (c *Cache) Start(gc shutdown.Gracefuller) {
	defer func() {
		c.stop()
		gc.Done()
	}()

	log.Info().Msg("[app] starting cache")

	waitCh := make(chan struct{})
	go func() {
		defer close(waitCh)
		c.db.Run()
		c.probe.Watch(c)
		c.server.Start()
	}()

	log.Info().Msg("[app] cache has been started")
	<-waitCh
}

// stop cancels context and dumps cache on shutdown.
func (c *Cache) stop() {
	log.Info().Msg("[app] stopping cache")
	defer c.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if c.cfg.Cache.Enabled && c.cfg.Cache.Persistence.Dump.IsEnabled {
		if err := c.dumper.Dump(ctx); err != nil {
			log.Err(err).Msg("[dump] failed to store cache dump")
		}
	}

	log.Info().Msg("[app] cache has been stopped")
}

func (c *Cache) LoadData(ctx context.Context, interactive bool) error {
	if interactive {
		if err := c.loadDataInteractive(ctx); err != nil {
			if !errors.Is(err, useYamlCfgErr) {
				return err
			}
			return c.LoadData(ctx, false)
		}
	} else {
		if c.cfg.Cache.Enabled {
			if c.cfg.Cache.Persistence.Dump.IsEnabled {
				if err := c.dumper.Load(ctx); err != nil {
					log.Warn().Err(err).Msg("[dump] failed to load dump")
				}
			}
			if c.cfg.Cache.Persistence.Mock.Enabled {
				storage.LoadMocks(ctx, c.cfg, c.backend, c.db, c.cfg.Cache.Persistence.Mock.Length)
			}
		}
	}
	return nil
}

var useYamlCfgErr = errors.New("user choose a yaml config file instead")

// LoadDataInteractive asks user to select dump by size desc, mocks, yaml config or exit.
func (c *Cache) loadDataInteractive(ctx context.Context) error {
	entries, err := os.ReadDir(c.cfg.Cache.Persistence.Dump.Dir)
	if err != nil {
		log.Fatal().Err(err).Msgf("[dump] failed to read dumps dir %q: %v", c.cfg.Cache.Persistence.Dump.Dir, err)
		return err
	}

	var versions []Version
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(c.cfg.Cache.Persistence.Dump.Dir, e.Name())
		info, err := os.Stat(full)
		if err != nil {
			log.Err(err).Msgf("[dump] warning: cannot stat %q: %v", full, err)
			continue
		}
		sz, err := c.dirSize(full)
		if err != nil {
			log.Err(err).Msgf("[dump] warning: cannot size %q: %v", full, err)
			continue
		}
		versions = append(versions, Version{e.Name(), sz, info.ModTime()})
	}
	if len(versions) == 0 {
		log.Err(err).Msgf("[dump] no versions in %q", c.cfg.Cache.Persistence.Dump.Dir)
		return err
	}

	// sort by size descending
	sort.Slice(versions, func(i, j int) bool { return versions[i].Size > versions[j].Size })

	// build menu: versions then custom actions
	items := make([]string, 0, len(versions)+4)
	for _, v := range versions {
		items = append(items, fmt.Sprintf("%s\t%s\t%s", v.Name, c.byteCount(v.Size), v.ModTime.Format(time.RFC3339)))
	}
	items = append(items,
		"Continue without dump loading",
		"Run mocks loading",
		"Run with yaml file config",
		"Exit",
	)

	prompt := promptui.Select{Label: "Select version or action", Items: items, Size: len(items)}
	idx, _, err := prompt.Run()
	if err != nil {
		log.Err(err).Msgf("[dump] selection aborted: %v", err)
		return err
	}

	n := len(versions)
	switch {
	case idx < n:
		// apply version
		ver := versions[idx]
		confirm := promptui.Prompt{Label: fmt.Sprintf("Apply version %s?", ver.Name), IsConfirm: true}
		if _, err := confirm.Run(); err != nil {
			fmt.Println("Aborted.")
			return err
		}
		fmt.Println(fmt.Sprintf("Applying dump %q…", ver.Name))
		if err := c.applyDump(ctx, ver); err != nil {
			log.Err(err).Msgf("[dump] failed apply %s: %v", ver.Name, err)
			return err
		}
		fmt.Println("Done.")
	case idx == n:
		fmt.Println("Continuing without dump.")
	case idx == n+1:
		// mocks: ask count
		lenPrompt := promptui.Prompt{Label: "Number of mocks to load", Validate: func(in string) error {
			if _, err := strconv.Atoi(in); err != nil {
				return fmt.Errorf("invalid: %v", err)
			}
			return nil
		}}
		res, _ := lenPrompt.Run()
		nMocks, _ := strconv.Atoi(res)
		storage.LoadMocks(ctx, c.cfg, c.backend, c.db, nMocks)
	case idx == n+2:
		// yaml config: prompt with default, existence check, load with fallback
		for {
			// initial prompt
			cfgPrompt := promptui.Prompt{
				Label:     fmt.Sprintf("Path to YAML config (type 'local' for use '%s' and 'main' for %s) or type 'back' for move to main menu", ConfigPathLocal, ConfigPath),
				AllowEdit: true,
			}
			path, _ := cfgPrompt.Run()
			if strings.TrimSpace(path) == "" {
				fmt.Println("Sorry, empty config path is not allowed, try again.")
				continue
			} else if strings.TrimSpace(path) == "back" {
				return c.loadDataInteractive(ctx)
			} else if "local" == strings.TrimSpace(path) {
				path = ConfigPathLocal
			} else if "main" == strings.TrimSpace(path) {
				path = ConfigPath
			}
			fmt.Println("Using config: ", path)

			// check file exists
			info, statErr := os.Stat(path)
			if statErr != nil {
				if os.IsNotExist(statErr) {
					fmt.Println(fmt.Sprintf("Config file %q not found. Please try again.", path))
					continue
				}
				fmt.Println(fmt.Sprintf("Error accessing %q: %v", path, statErr))
				continue
			} else if info.IsDir() {
				fmt.Println(fmt.Sprintf("%q is a directory, not a file. Please try again.", path))
				continue
			}

			// attempt to load
			cfg, err := config.LoadConfig(path)
			if err != nil {
				fmt.Println(fmt.Sprintf("Failed to load selected config: %v", err))
				if path == ConfigPathLocal {
					// fallback to system config
					cfg, err = config.LoadConfig(ConfigPath)
					if err != nil {
						fmt.Println(fmt.Sprintf("Failed to load fallback config: %v. Exit.", err))
						return err
					}
				} else {
					continue
				}
			}

			// success: apply new config
			c.cfg = cfg
			return useYamlCfgErr
		}
	case idx == n+3:
		fmt.Println("Exiting.")
		os.Exit(0)
	}
	return nil
}

func (c *Cache) dirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func (c *Cache) parseVer(name string) int {
	if strings.HasPrefix(name, "v") {
		if n, err := strconv.Atoi(name[1:]); err == nil {
			return n
		}
	}
	return int(^uint(0) >> 1)
}

func (c *Cache) byteCount(b int64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "kMGTPE"[exp])
}

func (c *Cache) applyDump(ctx context.Context, v Version) error {
	return c.dumper.LoadVersion(ctx, v.Name)
}

func (c *Cache) IsAlive(_ context.Context) bool {
	if !c.server.IsAlive() {
		log.Info().Msg("[app] http server has gone away")
		return false
	}
	return true
}
