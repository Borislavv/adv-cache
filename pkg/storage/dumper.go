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

func (d *Dump) Dump(ctx context.Context) error {
	start := time.Now()

	cfg := d.cfg.Cache.Persistence.Dump
	if !cfg.IsEnabled {
		return errDumpNotEnabled
	}

	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return fmt.Errorf("create dump dir: %w", err)
	}

	timestamp := time.Now().Format("20060102T150405")
	var wg sync.WaitGroup
	var successNum, errorNum int32

	d.shardedMap.WalkShards(func(shardKey uint64, shard *sharded.Shard[*model.Entry]) {
		wg.Add(1)
		go func(shardKey uint64) {
			defer wg.Done()

			filename := fmt.Sprintf("%s/%s-shard-%d-%s.dump", cfg.Dir, cfg.Name, shardKey, timestamp)
			tmpName := filename + ".tmp"

			f, err := os.Create(tmpName)
			if err != nil {
				log.Error().Err(err).Msg("[dump] create error")
				atomic.AddInt32(&errorNum, 1)
				return
			}
			defer f.Close()
			bw := bufio.NewWriterSize(f, 512*1024)

			shard.Walk(ctx, func(key uint64, entry *model.Entry) bool {
				data := entry.ToBytes()
				size := uint32(len(data))
				var scratch [4]byte
				binary.LittleEndian.PutUint32(scratch[:], size)
				bw.Write(scratch[:])
				bw.Write(data)
				atomic.AddInt32(&successNum, 1)
				return true
			}, true)

			bw.Flush()
			_ = f.Close()
			_ = os.Rename(tmpName, filename)
		}(shardKey)
	})

	wg.Wait()
	log.Info().Msgf("[dump] finished: %d entries, errors: %d, elapsed: %s", successNum, errorNum, time.Since(start))
	if errorNum > 0 {
		return fmt.Errorf("dump finished with %d errors", errorNum)
	}
	return nil
}

func (d *Dump) Load(ctx context.Context) error {
	start := time.Now()

	cfg := d.cfg.Cache.Persistence.Dump
	if !cfg.IsEnabled {
		return errDumpNotEnabled
	}

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
	var successNum, errorNum int32

	for _, file := range filesToLoad {
		wg.Add(1)
		go func(file string) {
			defer wg.Done()

			f, err := os.Open(file)
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
				size := binary.LittleEndian.Uint32(sizeBuf[:])
				buf := make([]byte, size)
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
				d.storage.Set(entry)
				atomic.AddInt32(&successNum, 1)

				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}(file)
	}

	wg.Wait()
	log.Info().Msgf("[dump] restored: %d entries, errors: %d, elapsed: %s", successNum, errorNum, time.Since(start))
	if errorNum > 0 {
		return fmt.Errorf("load finished with %d errors", errorNum)
	}
	return nil
}

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
