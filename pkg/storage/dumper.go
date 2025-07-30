package storage

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
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

var errDumpNotEnabled = errors.New("persistence mode is not enabled")

type Dumper interface {
	Dump(ctx context.Context) error
	Load(ctx context.Context) error
}

type Dump struct {
	cfg     *config.Cache
	storage Storage
	backend repository.Backender
}

func NewDumper(cfg *config.Cache, storage Storage, backend repository.Backender) *Dump {
	return &Dump{cfg, storage, backend}
}

func (d *Dump) Dump(ctx context.Context) error {
	start := time.Now()
	cfg := d.cfg.Cache.Persistence.Dump
	if !d.cfg.Cache.Enabled || !cfg.IsEnabled {
		return errDumpNotEnabled
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return fmt.Errorf("create base dump dir: %w", err)
	}

	versionDir := filepath.Join(cfg.Dir, fmt.Sprintf("v%d", nextVersionDir(cfg.Dir)))
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return fmt.Errorf("create version dir: %w", err)
	}
	timestamp := time.Now().Format("20060102T150405")
	var wg sync.WaitGroup
	var success, failures int32

	d.storage.WalkShards(ctx, func(shardKey uint64, shard *sharded.Shard[*model.Entry]) {
		wg.Add(1)
		go func(key uint64, s *sharded.Shard[*model.Entry]) {
			defer wg.Done()
			ext := ".dump"
			if cfg.Gzip {
				ext += ".gz"
			}
			name := fmt.Sprintf("%s/%s-shard-%d-%s%s", versionDir, cfg.Name, key, timestamp, ext)
			tmp := name + ".tmp"

			f, err := os.Create(tmp)
			if err != nil {
				log.Error().Err(err).Str("file", tmp).Msg("[dump] create error")
				atomic.AddInt32(&failures, 1)
				return
			}
			var (
				writer io.Writer = f
				gw     *gzip.Writer
			)
			if cfg.Gzip {
				gw = gzip.NewWriter(f)
				writer = gw
			}
			bw := bufio.NewWriterSize(writer, 512*1024)

			s.Walk(ctx, func(_ uint64, e *model.Entry) bool {
				data, release := e.ToBytes()
				defer release()
				var crc uint32
				if cfg.Crc32Control {
					crc = crc32.ChecksumIEEE(data)
				}

				var lenBuf [8]byte
				binary.LittleEndian.PutUint32(lenBuf[0:4], uint32(len(data)))
				binary.LittleEndian.PutUint32(lenBuf[4:8], crc)
				if _, err := bw.Write(lenBuf[:]); err != nil {
					atomic.AddInt32(&failures, 1)
					return true
				}
				if _, err := bw.Write(data); err != nil {
					atomic.AddInt32(&failures, 1)
					return true
				}
				atomic.AddInt32(&success, 1)
				return true
			}, true)

			_ = bw.Flush()
			if gw != nil {
				_ = gw.Close()
			}
			_ = f.Close()
			_ = os.Rename(tmp, name)
		}(shardKey, shard)
	})

	wg.Wait()
	if cfg.MaxVersions > 0 {
		rotateVersionDirs(cfg.Dir, cfg.MaxVersions)
	}

	log.Info().Msgf("[dump] finished: %d entries, errors: %d, elapsed: %s", success, failures, time.Since(start))
	if failures > 0 {
		return fmt.Errorf("dump finished with %d errors", failures)
	}
	return nil
}

func (d *Dump) Load(ctx context.Context) error {
	start := time.Now()
	cfg := d.cfg.Cache.Persistence.Dump
	if !d.cfg.Cache.Enabled || !cfg.IsEnabled {
		return errDumpNotEnabled
	}

	dir := getLatestVersionDir(cfg.Dir)
	if dir == "" {
		return fmt.Errorf("no versioned dump dirs found in %s", cfg.Dir)
	}

	pattern := filepath.Join(dir, fmt.Sprintf("%s-shard-*.dump*", cfg.Name))
	files, _ := filepath.Glob(pattern)
	if len(files) == 0 {
		return fmt.Errorf("no dump files found in %s", dir)
	}
	ts := extractLatestTimestamp(files)
	files = filterFilesByTimestamp(files, ts)

	var wg sync.WaitGroup
	var success, failures int32

	for _, file := range files {
		wg.Add(1)
		go func(fn string) {
			defer wg.Done()

			f, err := os.Open(fn)
			if err != nil {
				log.Error().Err(err).Str("file", fn).Msg("[load] open error")
				atomic.AddInt32(&failures, 1)
				return
			}
			defer f.Close()

			var reader io.Reader = f
			if strings.HasSuffix(fn, ".gz") {
				gzr, err := gzip.NewReader(f)
				if err != nil {
					log.Error().Err(err).Str("file", fn).Msg("[load] gzip open error")
					atomic.AddInt32(&failures, 1)
					return
				}
				defer gzr.Close()
				reader = gzr
			}

			br := bufio.NewReaderSize(reader, 512*1024)
			var metaBuf [8]byte
			for {
				if _, err := io.ReadFull(br, metaBuf[:]); err == io.EOF {
					break
				} else if err != nil {
					log.Error().Err(err).Str("file", fn).Msg("[load] read meta error")
					atomic.AddInt32(&failures, 1)
					break
				}

				sz := binary.LittleEndian.Uint32(metaBuf[0:4])
				expCRC := binary.LittleEndian.Uint32(metaBuf[4:8])
				buf := make([]byte, sz)
				if _, err := io.ReadFull(br, buf); err != nil {
					log.Error().Err(err).Str("file", fn).Msg("[load] read entry error")
					atomic.AddInt32(&failures, 1)
					break
				}
				if cfg.Crc32Control && crc32.ChecksumIEEE(buf) != expCRC {
					log.Error().Str("file", fn).Msg("[load] crc mismatch")
					atomic.AddInt32(&failures, 1)
					continue
				}
				e, err := model.EntryFromBytes(buf, d.cfg, d.backend)
				if err != nil {
					log.Error().Err(err).Str("file", fn).Msg("[load] entry decode error")
					atomic.AddInt32(&failures, 1)
					continue
				}
				d.storage.Set(e)
				atomic.AddInt32(&success, 1)
				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}(file)
	}

	wg.Wait()
	log.Info().Msgf("[dump] restored: %d entries, errors: %d, elapsed: %s", success, failures, time.Since(start))
	if failures > 0 {
		return fmt.Errorf("load finished with %d errors", failures)
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
