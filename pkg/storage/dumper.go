package storage

import (
	"bufio"
	"context"
	"encoding/gob"
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
	dumpEntryPool = sync.Pool{
		New: func() any { return new(dumpEntry) },
	}
	errDumpNotEnabled = errors.New("persistence mode is not enabled")
)

type dumpEntry struct {
	RulePath     []byte `json:"rulePath"`
	MapKey       uint64 `json:"mapKey"`
	ShardKey     uint64 `json:"shardKey"`
	Payload      []byte `json:"payload"`
	IsCompressed bool   `json:"isCompressed"`
	WillUpdateAt int64  `json:"willUpdateAt"`
}

type Dumper interface {
	Dump(ctx context.Context) error
	Load(ctx context.Context) error
}

type Dump struct {
	cfg        *config.Cache
	shardedMap *sharded.Map[*model.Entry]
	storage    Storage
	backend    repository.Backender
}

func NewDumper(cfg *config.Cache, sm *sharded.Map[*model.Entry], storage Storage, backend repository.Backender) *Dump {
	return &Dump{
		cfg:        cfg,
		shardedMap: sm,
		storage:    storage,
		backend:    backend,
	}
}

// Dump saves all shards to separate files (parallel safe).
func (d *Dump) Dump(ctx context.Context) error {
	cfg := d.cfg.Cache.Persistence.Dump
	if !cfg.IsEnabled {
		return errDumpNotEnabled
	}
	start := time.Now()

	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return fmt.Errorf("create dump dir: %w", err)
	}

	timestamp := time.Now().Format("20060102T150405")
	var wg sync.WaitGroup
	errCh := make(chan error, sharded.NumOfShards)
	var successNum, errorNum int32

	d.shardedMap.WalkShards(func(shardKey uint64, shard *sharded.Shard[*model.Entry]) {
		wg.Add(1)
		go func(shardKey uint64) {
			defer wg.Done()

			filename := fmt.Sprintf("%s/%s-shard-%d-%s.dump", cfg.Dir, cfg.Name, shardKey, timestamp)
			tmpName := filename + ".tmp"

			f, err := os.Create(tmpName)
			if err != nil {
				errCh <- fmt.Errorf("create dump file: %w", err)
				return
			}
			bw := bufio.NewWriterSize(f, 512*1024)
			enc := gob.NewEncoder(bw)

			shard.Walk(ctx, func(key uint64, entry *model.Entry) bool {
				e := dumpEntryPool.Get().(*dumpEntry)
				*e = dumpEntry{
					RulePath:     entry.Rule().PathBytes,
					MapKey:       entry.MapKey(),
					ShardKey:     entry.ShardKey(),
					Payload:      append([]byte(nil), entry.PayloadBytes()...), // defensive copy
					IsCompressed: entry.IsCompressed(),
					WillUpdateAt: entry.WillUpdateAt(),
				}
				if err := enc.Encode(e); err != nil {
					log.Error().Err(err).Msg("[dump] encode error")
					atomic.AddInt32(&errorNum, 1)
					errCh <- err
				} else {
					atomic.AddInt32(&successNum, 1)
				}
				*e = dumpEntry{}
				dumpEntryPool.Put(e)
				return true
			}, true)

			_ = bw.Flush()
			_ = f.Close()

			if err := os.Rename(tmpName, filename); err != nil {
				errCh <- fmt.Errorf("rename dump file: %w", err)
			}
		}(shardKey)
	})

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}

	if cfg.RotatePolicy == "ring" {
		if err := rotateOldFiles(cfg.Dir, cfg.Name, ".dump", cfg.MaxFiles); err != nil {
			log.Error().Err(err).Msg("[dump] rotation error")
		}
	}

	log.Info().Msgf("[dump] finished: %d entries, errors: %d, time: %s", successNum, errorNum, time.Since(start))
	if errorNum > 0 {
		return fmt.Errorf("dump finished with %d errors", errorNum)
	}
	return nil
}

// Load restores the latest dump set into storage.
func (d *Dump) Load(ctx context.Context) error {
	cfg := d.cfg.Cache.Persistence.Dump
	if !cfg.IsEnabled {
		return errDumpNotEnabled
	}

	start := time.Now()
	files, err := filepath.Glob(fmt.Sprintf("%s/%s-shard-*.dump", cfg.Dir, cfg.Name))
	if err != nil {
		return fmt.Errorf("glob error: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no dump files found in %s", cfg.Dir)
	}

	latestTs := extractLatestTimestamp(files)
	filesToLoad := filterFilesByTimestamp(files, latestTs)
	if len(filesToLoad) == 0 {
		return fmt.Errorf("no files for timestamp %s", latestTs)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(filesToLoad))
	var successNum, errorNum int32

	for _, file := range filesToLoad {
		wg.Add(1)
		go func(file string) {
			defer wg.Done()

			f, err := os.Open(file)
			if err != nil {
				errCh <- fmt.Errorf("open dump file: %w", err)
				return
			}
			defer f.Close()

			dec := gob.NewDecoder(bufio.NewReaderSize(f, 512*1024))
		loop:
			for {
				select {
				case <-ctx.Done():
					return
				default:
					e := dumpEntryPool.Get().(*dumpEntry)
					if err = dec.Decode(e); err == io.EOF {
						dumpEntryPool.Put(e)
						break loop
					} else if err != nil {
						log.Error().Err(err).Msg("[dump] decode error")
						dumpEntryPool.Put(e)
						atomic.AddInt32(&errorNum, 1)
						break loop
					}

					entry, err := d.buildResponseFromEntry(e)
					if err != nil {
						log.Error().Err(err).Msg("[dump] rebuild failed")
						dumpEntryPool.Put(e)
						atomic.AddInt32(&errorNum, 1)
						continue
					}

					d.storage.Set(entry)
					dumpEntryPool.Put(e)
					atomic.AddInt32(&successNum, 1)
				}
			}
		}(file)
	}

	wg.Wait()
	close(errCh)

	log.Info().Msgf("[dump] restored: %d entries, errors: %d, time: %s", successNum, errorNum, time.Since(start))
	if errorNum > 0 {
		return fmt.Errorf("load finished with %d errors", errorNum)
	}
	return nil
}

func (d *Dump) buildResponseFromEntry(e *dumpEntry) (*model.Entry, error) {
	rule := model.MatchRule(d.cfg, e.RulePath)
	if rule == nil {
		return nil, fmt.Errorf("rule not found for path: %s", string(e.RulePath))
	}
	return model.NewEntryFromField(
		e.MapKey, e.ShardKey, e.Payload, rule,
		d.backend.RevalidatorMaker(), e.IsCompressed, e.WillUpdateAt,
	), nil
}

// --- Helpers

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

func filterFilesByTimestamp(files []string, ts string) []string {
	var out []string
	for _, f := range files {
		if strings.Contains(f, ts) {
			out = append(out, f)
		}
	}
	return out
}

func rotateOldFiles(dir, baseName, ext string, maxFiles int) error {
	files, err := filepath.Glob(filepath.Join(dir, baseName+".*"+ext))
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool {
		fi, _ := os.Stat(files[i])
		fj, _ := os.Stat(files[j])
		return fi.ModTime().Before(fj.ModTime())
	})
	if len(files) <= maxFiles {
		return nil
	}
	for _, f := range files[:len(files)-(maxFiles-1)] {
		if err := os.Remove(f); err != nil {
			log.Error().Err(err).Msgf("[dump] failed to remove old dump file %s", f)
		}
	}
	return nil
}
