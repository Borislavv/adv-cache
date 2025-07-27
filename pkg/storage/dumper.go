package storage

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/Borislavv/advanced-cache/pkg/repository"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"github.com/rs/zerolog/log"
)

var (
	errDumpNotEnabled = errors.New("persistence mode is not enabled")
)

// Dumper defines dump & load operations.
type Dumper interface {
	Dump(ctx context.Context) error
	Load(ctx context.Context) error
}

// Dump implements persistence with versioned directories.
type Dump struct {
	cfg     *config.Cache
	storage Storage
	backend repository.Backender
}

// NewDumper constructs a Dump.
func NewDumper(cfg *config.Cache, storage Storage, backend repository.Backender) *Dump {
	return &Dump{
		cfg:     cfg,
		storage: storage,
		backend: backend,
	}
}

// Dump writes all entries into a new versioned directory.
// It then rotates old dirs, keeping only the latest cfg.MaxVersions.
func (d *Dump) Dump(ctx context.Context) error {
	start := time.Now()
	cfg := d.cfg.Cache.Persistence.Dump
	if !d.cfg.Cache.Enabled || !cfg.IsEnabled {
		return errDumpNotEnabled
	}

	// Ensure base dir exists
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return fmt.Errorf("create base dump dir: %w", err)
	}

	// Determine new version dir
	version := nextVersionDir(cfg.Dir)
	versionDir := filepath.Join(cfg.Dir, fmt.Sprintf("v%d", version))
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return fmt.Errorf("create version dir: %w", err)
	}

	timestamp := time.Now().Format("20060102T150405")
	var wg sync.WaitGroup
	var successNum, errorNum int32

	// Parallel dump shards
	d.storage.WalkShards(func(shardKey uint64, shard *sharded.Shard[*model.VersionPointer]) {
		wg.Add(1)
		go func(sh uint64) {
			defer wg.Done()

			filename := fmt.Sprintf("%s/%s-shard-%d-%s.dump",
				versionDir, cfg.Name, sh, timestamp)
			tmpName := filename + ".tmp"

			f, err := os.Create(tmpName)
			if err != nil {
				log.Error().Err(err).Msg("[dump] create error")
				atomic.AddInt32(&errorNum, 1)
				return
			}
			defer f.Close()

			bw := bufio.NewWriterSize(f, 512*1024)
			shard.Walk(ctx, func(key uint64, entry *model.VersionPointer) bool {
				if !entry.Acquire() {
					return true
				}
				defer entry.Release()

				data, releaser := entry.ToBytes()
				defer releaser()

				// write length + data
				var lenBuf [4]byte
				binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(data)))
				bw.Write(lenBuf[:])
				bw.Write(data)
				atomic.AddInt32(&successNum, 1)
				return true
			}, true)

			bw.Flush()
			f.Close() // ensure file closed before rename
			os.Rename(tmpName, filename)
		}(shardKey)
	})

	wg.Wait()

	// Rotate old version dirs, keeping only latest MaxVersions
	if cfg.MaxVersions > 0 {
		rotateVersionDirs(cfg.Dir, cfg.MaxVersions)
	}

	log.Info().
		Msgf("[dump] finished: %d entries, errors: %d, elapsed: %s",
			successNum, errorNum, time.Since(start))

	if errorNum > 0 {
		return fmt.Errorf("dump finished with %d errors", errorNum)
	}
	return nil
}

// Load reads from the latest versioned dir; if none, falls back to mock.
func (d *Dump) Load(ctx context.Context) error {
	var successNum int32

	start := time.Now()
	cfg := d.cfg.Cache.Persistence.Dump
	if !d.cfg.Cache.Enabled || !cfg.IsEnabled {
		return errDumpNotEnabled
	}

	latestDir := getLatestVersionDir(cfg.Dir)
	if latestDir == "" {
		return fmt.Errorf("no versioned dump dirs found in %s", cfg.Dir)
	}

	pattern := fmt.Sprintf("%s-shard-*.dump", cfg.Name)
	files, err := filepath.Glob(filepath.Join(latestDir, pattern))
	if err != nil {
		return fmt.Errorf("glob error: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no dump files found in %s", latestDir)
	}

	latestTs := extractLatestTimestamp(files)
	toLoad := filterFilesByTimestamp(files, latestTs)

	var wg sync.WaitGroup
	var errorNum int32

	for _, file := range toLoad {
		wg.Add(1)
		go func(fn string) {
			defer wg.Done()
			f, err := os.Open(fn)
			if err != nil {
				log.Error().Err(err).Msg("[load] open error")
				atomic.AddInt32(&errorNum, 1)
				return
			}
			defer f.Close()

			br := bufio.NewReaderSize(f, 512*1024)
			var sizeBuf [4]byte
			for {
				if _, err := io.ReadFull(br, sizeBuf[:]); err == io.EOF {
					break
				} else if err != nil {
					log.Error().Err(err).Msg("[load] read size error")
					atomic.AddInt32(&errorNum, 1)
					break
				}
				sz := binary.LittleEndian.Uint32(sizeBuf[:])
				buf := make([]byte, sz)
				if _, err := io.ReadFull(br, buf); err != nil {
					log.Error().Err(err).Msg("[load] read entry error")
					atomic.AddInt32(&errorNum, 1)
					break
				}
				entry, err := model.EntryFromBytes(buf, d.cfg, d.backend)
				if err != nil {
					log.Error().Err(err).Msg("[load] entry decode error")
					atomic.AddInt32(&errorNum, 1)
					continue
				}
				if persistedEntry, wasPersisted := d.storage.Set(model.NewVersionPointer(entry)); wasPersisted {
					atomic.AddInt32(&successNum, 1)
					persistedEntry.Release()
				} else {
					atomic.AddInt32(&errorNum, 1)
					log.Error().Msg("[dump] failed to persist entry, not enough memory")
					persistedEntry.Remove()
				}

				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}(file)
	}

	wg.Wait()
	log.Info().
		Msgf("[dump] restored: %d entries, errors: %d, elapsed: %s",
			successNum, errorNum, time.Since(start))
	if errorNum > 0 {
		return fmt.Errorf("load finished with %d errors", errorNum)
	}
	return nil
}

// nextVersionDir picks the next sequential version number.
func nextVersionDir(baseDir string) int {
	entries, _ := filepath.Glob(filepath.Join(baseDir, "v*"))
	maxV := 0
	for _, dir := range entries {
		name := filepath.Base(dir)
		if !strings.HasPrefix(name, "v") {
			continue
		}
		var v int
		fmt.Sscanf(name, "v%d", &v)
		if v > maxV {
			maxV = v
		}
	}
	return maxV + 1
}

// rotateVersionDirs keeps only the newest `max` dirs, removes the rest.
func rotateVersionDirs(baseDir string, max int) {
	entries, _ := filepath.Glob(filepath.Join(baseDir, "v*"))
	if len(entries) <= max {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		fi, _ := os.Stat(entries[i])
		fj, _ := os.Stat(entries[j])
		return fi.ModTime().After(fj.ModTime())
	})
	for _, dir := range entries[max:] {
		os.RemoveAll(dir)
		log.Info().Msgf("[dump] removed old dump dir: %s", dir)
	}
}

// getLatestVersionDir returns the most recently modified version dir.
func getLatestVersionDir(baseDir string) string {
	entries, _ := filepath.Glob(filepath.Join(baseDir, "v*"))
	if len(entries) == 0 {
		return ""
	}
	sort.Slice(entries, func(i, j int) bool {
		fi, _ := os.Stat(entries[i])
		fj, _ := os.Stat(entries[j])
		return fi.ModTime().After(fj.ModTime())
	})
	return entries[0]
}

// extractLatestTimestamp picks the largest timestamp suffix among files.
func extractLatestTimestamp(files []string) string {
	var tsList []string
	for _, f := range files {
		parts := strings.Split(filepath.Base(f), "-")
		if len(parts) >= 4 {
			ts := strings.TrimSuffix(parts[len(parts)-1], ".dump")
			tsList = append(tsList, ts)
		}
	}
	sort.Strings(tsList)
	if len(tsList) == 0 {
		return ""
	}
	return tsList[len(tsList)-1]
}

// filterFilesByTimestamp returns only files containing the given ts.
func filterFilesByTimestamp(files []string, ts string) []string {
	var out []string
	for _, f := range files {
		if strings.Contains(f, ts) {
			out = append(out, f)
		}
	}
	return out
}
