package lru

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
	"github.com/rs/zerolog/log"
)

var dumpEntryPool = sync.Pool{
	New: func() any { return new(dumpEntry) },
}

var dumpIsNotEnabledErr = errors.New("persistence mode is not enabled")

type dumpEntry struct {
	Unique     string      `json:"unique"`
	StatusCode int         `json:"statusCode"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
	Query      []byte      `json:"query"`
	Path       []byte      `json:"path"`
	MapKey     uint64      `json:"mapKey"`
	ShardKey   uint64      `json:"shardKey"`
}

// nopWriteCloser wraps an io.Writer to satisfy io.WriteCloser
// with a no-op Close method.
type nopWriteCloser struct {
	io.Writer
}

func (n nopWriteCloser) Close() error { return nil }

// DumpToDir writes cache entries to disk based on the configured format and rotation policy.
func (c *Storage) DumpToDir(ctx context.Context) error {
	dumpCfg := c.cfg.Cache.Persistence.Dump
	if !dumpCfg.IsEnabled {
		return dumpIsNotEnabledErr
	}
	start := time.Now()
	// Ensure directory exists
	if err := os.MkdirAll(dumpCfg.Dir, 0o755); err != nil {
		return fmt.Errorf("create dump dir: %w", err)
	}

	// Determine extension & writer wrapper
	ext := ".json"
	wrapWriter := func(w io.Writer) (io.WriteCloser, error) {
		return nopWriteCloser{w}, nil
	}
	if dumpCfg.Format == "gzip" {
		ext = ".gz"
		wrapWriter = func(w io.Writer) (io.WriteCloser, error) {
			return gzip.NewWriter(w), nil
		}
	}

	// Rotation logic
	var finalName string
	if dumpCfg.RotatePolicy == "ring" {
		if err := rotateOldFiles(dumpCfg.Dir, dumpCfg.Name, ext, dumpCfg.MaxFiles); err != nil {
			log.Err(err).Msg("[dump] rotation error")
		}
		timestamp := time.Now().Format("20060102T150405")
		finalName = fmt.Sprintf("%s.%s%s", dumpCfg.Name, timestamp, ext)
	} else {
		finalName = dumpCfg.Name + ext
	}
	filename := filepath.Join(dumpCfg.Dir, finalName)
	tmpName := filename + ".tmp"

	// Create temp file
	f, err := os.Create(tmpName)
	if err != nil {
		return fmt.Errorf("create dump temp file: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmpName)
	}()

	// Wrap writer
	wc, err := wrapWriter(f)
	if err != nil {
		return fmt.Errorf("wrap writer: %w", err)
	}
	// Ensure gzip writer is closed
	if gzW, ok := wc.(*gzip.Writer); ok {
		defer gzW.Close()
	}
	bw := bufio.NewWriterSize(wc, 64*1024*1024)
	defer bw.Flush()

	enc := json.NewEncoder(bw)
	var successNum, errorNum int32
	mu := sync.Mutex{}

	// Walk and encode entries
	c.shardedMap.WalkShards(func(shardKey uint64, shard *sharded.Shard[*model.Response]) {
		shard.Walk(ctx, func(key uint64, resp *model.Response) bool {
			mu.Lock()
			defer mu.Unlock()

			e := dumpEntryPool.Get().(*dumpEntry)
			*e = dumpEntry{
				Unique:     fmt.Sprintf("%d-%d", shardKey, key),
				StatusCode: resp.Data().StatusCode(),
				Headers:    resp.Data().Headers(),
				Body:       resp.Data().Body(),
				Query:      resp.Request().ToQuery(),
				Path:       resp.Request().Path(),
				MapKey:     resp.Request().MapKey(),
				ShardKey:   resp.Request().ShardKey(),
			}

			if err := enc.Encode(e); err != nil {
				log.Err(err).Msg("[dump] entry encode error")
				atomic.AddInt32(&errorNum, 1)
			} else {
				atomic.AddInt32(&successNum, 1)
			}

			// Clean and put back
			*e = dumpEntry{}
			dumpEntryPool.Put(e)
			return true
		}, true)
	})

	// Finalize file
	if err := os.Rename(tmpName, filename); err != nil {
		return fmt.Errorf("rename dump file: %w", err)
	}

	log.Info().Msgf("[dump] finished writing %d entries, errors: %d (elapsed: %s)", successNum, errorNum, time.Since(start))
	if errorNum > 0 {
		return fmt.Errorf("dump completed with %d errors", errorNum)
	}
	return nil
}

// LoadFromDir restores cache entries from disk based on configuration.
func (c *Storage) LoadFromDir(ctx context.Context) error {
	dumpCfg := c.cfg.Cache.Persistence.Dump
	if !dumpCfg.IsEnabled {
		return dumpIsNotEnabledErr
	}
	start := time.Now()

	ext := ".json"
	wrapReader := func(r io.Reader) (io.ReadCloser, error) {
		return io.NopCloser(r), nil
	}
	if dumpCfg.Format == "gzip" {
		ext = ".gz"
		wrapReader = func(r io.Reader) (io.ReadCloser, error) {
			return gzip.NewReader(r)
		}
	}

	// Select file to load
	var filename string
	if dumpCfg.RotatePolicy == "ring" {
		files, err := getDumpFiles(dumpCfg.Dir, dumpCfg.Name, ext)
		if err != nil {
			return fmt.Errorf("get dump files: %w", err)
		}
		if len(files) == 0 {
			return fmt.Errorf("no dump files found in %s", dumpCfg.Dir)
		}
		sorted, err := sortByModTime(files)
		if err != nil {
			return fmt.Errorf("sort dump files: %w", err)
		}
		filename = sorted[len(sorted)-1]
	} else {
		filename = filepath.Join(dumpCfg.Dir, dumpCfg.Name+ext)
	}

	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open dump file: %w", err)
	}
	defer f.Close()

	rc, err := wrapReader(f)
	if err != nil {
		return fmt.Errorf("wrap reader: %w", err)
	}
	defer rc.Close()

	dec := json.NewDecoder(bufio.NewReaderSize(rc, 64*1024*1024))
	var successNum, errorNum int
	for dec.More() {
		if ctx.Err() != nil {
			log.Warn().Msg("[dump] context cancelled")
			return ctx.Err()
		}

		entry := dumpEntryPool.Get().(*dumpEntry)
		if err := dec.Decode(entry); err != nil {
			log.Err(err).Msg("[dump] entry decode error")
			errorNum++
			dumpEntryPool.Put(entry)
			continue
		}

		data := model.NewData(c.cfg, entry.Path, entry.StatusCode, entry.Headers, entry.Body)
		req := model.NewRawRequest(c.cfg, entry.MapKey, entry.ShardKey, entry.Query, entry.Path)
		resp, err := model.NewResponse(data, req, c.cfg, c.backend.RevalidatorMaker(req))
		if err != nil {
			log.Err(err).Msg("[dump] response build failed")
			errorNum++
			dumpEntryPool.Put(entry)
			continue
		}
		c.Set(resp)
		successNum++
		dumpEntryPool.Put(entry)
	}

	log.Info().Msgf("[dump] restored %d entries, errors: %d (elapsed: %s)", successNum, errorNum, time.Since(start))
	if errorNum > 0 {
		return fmt.Errorf("load completed with %d errors", errorNum)
	}
	return nil
}

// getDumpFiles returns all dump files matching baseName.*ext in dir.
func getDumpFiles(dir, baseName, ext string) ([]string, error) {
	pattern := filepath.Join(dir, baseName+".*"+ext)
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	return files, nil
}

// sortByModTime returns the paths sorted by modification time ascending.
func sortByModTime(files []string) ([]string, error) {
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	infos := make([]fileInfo, 0, len(files))
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		infos = append(infos, fileInfo{path: f, modTime: fi.ModTime()})
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].modTime.Before(infos[j].modTime)
	})
	sorted := make([]string, len(infos))
	for i, info := range infos {
		sorted[i] = info.path
	}
	return sorted, nil
}

// rotateOldFiles removes oldest files so that after removal, count <= maxFiles-1.
func rotateOldFiles(dir, baseName, ext string, maxFiles int) error {
	files, err := getDumpFiles(dir, baseName, ext)
	if err != nil {
		return err
	}
	sorted, err := sortByModTime(files)
	if err != nil {
		return err
	}
	if len(sorted) < maxFiles {
		return nil
	}
	// Remove oldest so that count <= maxFiles-1
	numToRemove := len(sorted) - (maxFiles - 1)
	for i := 0; i < numToRemove; i++ {
		if err := os.Remove(sorted[i]); err != nil {
			log.Err(err).Msgf("[dump] failed to remove old dump file %s", sorted[i])
		}
	}
	return nil
}
