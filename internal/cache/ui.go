package cache

import (
	"context"
	"errors"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/manifoldco/promptui"
	"github.com/rs/zerolog/log"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	cfgFilePatternYml  = "advCache.cfg.*.yml"
	cfgFilePatternYaml = "advCache.cfg.*.yaml"
)

func wannaContinueAfterError(msg string, err error) bool {
	prompt := promptui.Prompt{
		Label:     fmt.Sprintf("%s: %v. Continue anyway?", msg, err),
		IsConfirm: true,
	}
	if _, promptErr := prompt.Run(); promptErr != nil {
		fmt.Println("Aborted.")
		return false
	}
	fmt.Println("Continuing...")
	return true
}

func (c *Cache) loadData(ctx context.Context, interactive bool) error {
	if interactive {
		if err := c.loadDataInteractive(ctx); err != nil {
			if !errors.Is(err, useYamlCfgErr) {
				return err
			}
			return c.loadData(ctx, false)
		}

		savePrompt := promptui.Prompt{Label: "Save cache state to new dump version on termination?", IsConfirm: true}
		// if user confirms, comment: yes -> new dump will be stored
		if _, spErr := savePrompt.Run(); spErr == nil {
			c.cfg.Data().Dump.IsEnabled = true
		}
	} else {
		if c.cfg.IsEnabled() {
			if c.cfg.Data().Dump.IsEnabled {
				if err := c.db.Load(ctx); err != nil {
					log.Warn().Err(err).Msg("[dump] failed to load dump")
				}
			} else {
				log.Info().Msg("[app] dump loading is disabled")
			}
			if c.cfg.Data().Mock.Enabled {
				storage.LoadMocks(ctx, c.cfg, c.db, c.cfg.Data().Mock.Length)
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
	entries, err := os.ReadDir(c.cfg.Data().Dump.Dir)
	if err != nil {
		log.Fatal().Err(err).Msgf("[dump] failed to read dumps dir %q: %v", c.cfg.Data().Dump.Dir, err)
		return err
	}

	var versions []Version
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(c.cfg.Data().Dump.Dir, e.Name())
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
		log.Err(err).Msgf("[dump] no versions in '%s'", c.cfg.Data().Dump.Dir)
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
		storage.LoadMocks(ctx, c.cfg, c.db, nMocks)

	case idx == n+2:
		// YAML config selection menu: search for advCache.cfg.*.yml and .yaml in the current dir
		for {
			// Collect matching files
			ymlFiles, _ := filepath.Glob(cfgFilePatternYml)
			yamlFiles, _ := filepath.Glob(cfgFilePatternYaml)
			configs := append(ymlFiles, yamlFiles...)

			// If none found, return to the previous menu
			if len(configs) == 0 {
				fmt.Println("No advCache.cfg.*.yml/.yaml config files found")
				return c.loadDataInteractive(ctx)
			}

			// Build menu items: all config files + “Back”
			items := make([]string, len(configs)+1)
			for i, cfgPath := range configs {
				items[i] = cfgPath
			}
			items[len(configs)] = "Back"

			prompt := promptui.Select{
				Label: "Select config file",
				Items: items,
			}
			index, _, err := prompt.Run()
			if err != nil {
				log.Err(err).Msgf("[app] config selection aborted: %v", err)
				return err
			}

			// “Back” chosen → go back
			if index == len(items)-1 {
				return c.loadDataInteractive(ctx)
			}

			// Try loading the selected config
			selected := items[index]
			fmt.Printf("Using config: %s\n", selected)
			newCfg, loadErr := config.LoadConfig(selected)
			if loadErr != nil {
				fmt.Printf("Failed to load config %q: %v\n", selected, loadErr)
				continue
			}

			newApp, err := NewApp(c.ctx, newCfg, c.probe, c.isInteractive)
			if err != nil {
				if wannaContinueAfterError("Application init. failed", err) {
					return c.loadDataInteractive(ctx)
				}
				return err
			}
			*c = *newApp
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
			fmt.Printf("Config reloaded from %s\n", path)

			newApp, err := NewApp(c.ctx, newCfg, c.probe, c.isInteractive)
			if err != nil {
				if wannaContinueAfterError("Application init. failed", err) {
					return c.loadDataInteractive(ctx)
				}
				return err
			}
			*c = *newApp

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
	return c.db.LoadVersion(ctx, v.Name)
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
