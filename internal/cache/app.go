package cache

import (
	"context"
	"errors"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/upstream"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Borislavv/advanced-cache/internal/cache/server"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/k8s/probe/liveness"
	"github.com/Borislavv/advanced-cache/pkg/prometheus/metrics"
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
	backend upstream.Upstream
	db      storage.Storage
}

// NewApp builds a new Cache app.
func NewApp(ctx context.Context, baseCfg *config.Cache, probe liveness.Prober) (cache *Cache, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	var cfg config.Config
	cfg = config.MakeConfigAtomic(baseCfg)

	var upstreamCluster upstream.Upstream
	upstreamCluster, err = upstream.NewBackendsCluster(ctx, cfg)
	if err != nil {
		return nil, err
	}
	db := lru.NewStorage(ctx, cfg, upstreamCluster)
	dumper := storage.NewDumper(cfg, db, upstreamCluster)
	meter := metrics.New()

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

	if c.cfg.Enabled && c.cfg.Persistence.Dump.IsEnabled {
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

		savePrompt := promptui.Prompt{Label: "Save cache state to new dump version on termination?", IsConfirm: true}
		// if user confirms, comment: yes -> new dump will be stored
		if _, spErr := savePrompt.Run(); spErr == nil {
			c.cfg.Persistence.Dump.IsEnabled = true
		}
	} else {
		if c.cfg.Enabled {
			if c.cfg.Persistence.Dump.IsEnabled {
				if err := c.dumper.Load(ctx); err != nil {
					log.Warn().Err(err).Msg("[dump] failed to load dump")
				}
			} else {
				log.Info().Msg("[app] dump loading is disabled")
			}
			if c.cfg.Persistence.Mock.Enabled {
				storage.LoadMocks(ctx, c.cfg, c.backend, c.db, c.cfg.Persistence.Mock.Length)
			} else {
				log.Info().Msg("[app] mock loading is disabled")
			}
		} else {
			log.Info().Msg("[app] cache is disabled")
		}
	}
	return nil
}

var useYamlCfgErr = errors.New("user choose a yaml config file instead")

// loadDataInteractive asks user to select dump by date desc, mocks, yaml config, edit or exit.
func (c *Cache) loadDataInteractive(ctx context.Context) error {
	entries, err := os.ReadDir(c.cfg.Persistence.Dump.Dir)
	if err != nil {
		log.Fatal().Err(err).Msgf("[dump] failed to read dumps dir %q: %v", c.cfg.Persistence.Dump.Dir, err)
		return err
	}

	var versions []Version
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(c.cfg.Persistence.Dump.Dir, e.Name())
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
		log.Err(err).Msgf("[dump] no versions in '%s'", c.cfg.Persistence.Dump.Dir)
	} else {
		// sort by ModTime descending
		sort.Slice(versions, func(i, j int) bool { return versions[i].ModTime.After(versions[j].ModTime) })
	}

	// build menu
	items := make([]string, 0, len(versions)+5)
	for _, v := range versions {
		items = append(items, fmt.Sprintf("%s\t%s\t%s", v.Name, c.byteCount(v.Size), v.ModTime.Format(time.RFC3339)))
	}
	items = append(items,
		"Continue without dump loading",
		"Run mocks loading",
		"Run yaml file config",
		"Edit and run yaml config",
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
		applyVersionPrompt := promptui.Prompt{Label: fmt.Sprintf("Apply version %s?", ver.Name), IsConfirm: true}
		if _, err := applyVersionPrompt.Run(); err != nil {
			fmt.Println("Aborted.")
			return err
		}
		fmt.Printf("Applying dump %q…\n", ver.Name)
		if err := c.applyDump(ctx, ver); err != nil {
			log.Err(err).Msgf("[dump] failed apply %s: %v", ver.Name, err)
			return err
		}

	case idx == n:
		fmt.Println("Continuing without dump.")

	case idx == n+1:
		numberOfMocksPrompt := promptui.Prompt{Label: "Number of mocks to load", Validate: func(in string) error {
			if _, err := strconv.Atoi(in); err != nil {
				return fmt.Errorf("invalid: %v", err)
			}
			return nil
		}}
		// mocks
		res, _ := numberOfMocksPrompt.Run()
		nMocks, _ := strconv.Atoi(res)
		storage.LoadMocks(ctx, c.cfg, c.backend, c.db, nMocks)

	case idx == n+2:
		// YAML config selection menu
		for {
			actions := []string{
				fmt.Sprintf("Local config (%s)", ConfigPathLocal),
				fmt.Sprintf("Prod config (%s)", ConfigPath),
				"Back",
				"Exit",
			}
			cfgPrompt := promptui.Select{Label: "Select config action", Items: actions}
			choice, _, err := cfgPrompt.Run()
			if err != nil {
				log.Err(err).Msgf("[dump] config selection aborted: %v", err)
				return err
			}

			var path string
			switch choice {
			case 0:
				path = ConfigPathLocal
			case 1:
				path = ConfigPath
			case 2:
				return c.loadDataInteractive(ctx)
			case 3:
				fmt.Println("Exiting.")
				os.Exit(0)
			}

			fmt.Printf("Using config: %s\n", path)
			info, statErr := os.Stat(path)
			if statErr != nil {
				if os.IsNotExist(statErr) {
					fmt.Printf("File %q not found\n", path)
					continue
				}
				return statErr
			}
			if info.IsDir() {
				fmt.Printf("%q is a directory\n", path)
				continue
			}
			cfg, loadErr := config.LoadConfig(path)
			if loadErr != nil {
				fmt.Printf("Failed to load config: %v\n", loadErr)
				continue
			}
			c.cfg = cfg
			return useYamlCfgErr
		}

	case idx == n+3:
		// Edit YAML config with fallback editors
		path, err := c.getCfgPath()
		if err != nil {
			return err
		}
		fmt.Println() // reset prompt
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "nano"
		}
		// Try user editor, then fallback to nano and vi
		for _, ed := range uniqueStrings([]string{editor, "nano", "vi"}) {
			fmt.Printf("Opening %s in %s...\n", path, ed)
			cmd := exec.Command(ed, path)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if runErr := cmd.Run(); runErr != nil {
				fmt.Printf("Editor %s error: %v\n", ed, runErr)
				continue
			}
			// success
			newCfg, loadErr := config.LoadConfig(path)
			if loadErr != nil {
				fmt.Printf("Failed to reload config: %v\n", loadErr)
				return loadErr
			}
			c.cfg = newCfg
			fmt.Printf("Config reloaded from %s\n", path)
			return useYamlCfgErr
		}
		// all editors failed
		return c.loadDataInteractive(ctx)

	case idx == n+4:
		fmt.Println("Exiting.")
		os.Exit(0)
	}
	return nil
}

func (c *Cache) getCfgPath() (string, error) {
	path := ConfigPathLocal
	_, statErr := os.Stat(path)
	if statErr != nil {
		path = ConfigPath
		_, statErr = os.Stat(path)
		if statErr != nil {
			return "", statErr
		}
	}
	return path, nil
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

func uniqueStrings(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, s := range input {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}
