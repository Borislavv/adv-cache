package lru

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
	"github.com/rs/zerolog/log"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	dumpFileName = "cache.dump"
	filePerm     = 0644
)

type dumpEntry struct {
	Unique     string      `json:"unique"`
	StatusCode int         `json:"statusCode"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
	Project    []byte      `json:"project"`  // Interned project name
	Domain     []byte      `json:"domain"`   // Interned domain
	Language   []byte      `json:"language"` // Interned language
	KeyBuf     []byte      `json:"keyBuf"`
	Tags       [][]byte    `json:"tags"` // Array of interned tag values
}

// DumpToDir writes all shards into a single binary file with length-prefixed records.
func (c *Storage) DumpToDir(ctx context.Context, dir string) error {
	log.Info().Msg("[dump] dumping storage has been started")
	defer log.Info().Msg("[dump] dumping storage has been finished")

	start := time.Now()
	filename := filepath.Join(dir, dumpFileName)

	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create dump file error: %w", err)
	}
	defer func() {
		if err = f.Close(); err != nil {
			log.Error().Err(err).Str("filename", filename).Msg("close dump file error")
		}
	}()

	w := bufio.NewWriterSize(f, 512*1024*1024) // 512MB buffer
	defer func() {
		if err = w.Flush(); err != nil {
			log.Err(err).Msg("flush writer error")
		}
		if err = f.Sync(); err != nil {
			log.Err(err).Msg("sync file error")
		}
	}()

	mu := &sync.Mutex{}
	var success, errors int32
	c.shardedMap.WalkShards(func(shardKey uint64, shard *sharded.Shard[*model.Response]) {
		shard.Walk(ctx, func(key uint64, resp *model.Response) bool {
			mu.Lock()
			defer mu.Unlock()
			b, merr := json.Marshal(&dumpEntry{
				Unique:     fmt.Sprintf("%d-%d", shardKey, key),
				StatusCode: resp.Data().StatusCode(),
				Headers:    resp.Data().Headers(),
				Body:       resp.Data().Body(),
				Project:    resp.Request().GetProject(),
				Domain:     resp.Request().GetDomain(),
				Language:   resp.Request().GetLanguage(),
				Tags:       resp.Request().GetTags(),
				KeyBuf:     resp.Request().KeyBuf,
			})
			if merr != nil {
				log.Err(merr).Msg("failed to marshal dump entry")
				atomic.AddInt32(&errors, 1)
				return true
			}

			if _, err = w.Write(b); err != nil {
				log.Err(err).Msg("failed to write dump entry")
				atomic.AddInt32(&errors, 1)
				return true
			}
			if err = w.WriteByte('\n'); err != nil {
				log.Err(err).Msg("failed to write newline")
				atomic.AddInt32(&errors, 1)
				return true
			}

			atomic.AddInt32(&success, 1)

			return true
		}, true)
	})

	log.Info().Msgf("[dump] written %d keys, errors: %d (elapsed: %s)", success, errors, time.Since(start))
	if errors > 0 {
		return fmt.Errorf("dump completed with %d errors", errors)
	}
	return nil
}

// LoadFromDir loads all entries from dump file and restores them into the storage.
func (c *Storage) LoadFromDir(ctx context.Context, dir string) error {
	start := time.Now()
	filename := filepath.Join(dir, dumpFileName)

	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open dump file error: %w", err)
	}
	defer func() { _ = f.Close() }()

	decoder := json.NewDecoder(bufio.NewReaderSize(f, 256*1024*1024)) // 256MB

	var lineNum, success, failed int
	for {
		lineNum++

		if ctx.Err() != nil {
			log.Warn().Msg("[dump] context cancelled")
			return ctx.Err()
		}

		var raw json.RawMessage
		if err = decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			log.Err(err).Int("line", lineNum).Msg("[dump] decode raw error")
			continue
		}

		var entry = &dumpEntry{}
		if err = json.Unmarshal(raw, entry); err != nil {
			log.Err(err).Msg("[dump] failed to decode json entry")
			failed++
			continue
		}

		data := model.NewData(entry.StatusCode, entry.Headers, entry.Body)
		req, err := model.NewManualRequest(entry.Project, entry.Domain, entry.Language, entry.Tags)
		if err != nil {
			log.Err(err).Msg("[dump] failed to create request")
			failed++
			continue
		}

		resp, err := model.NewResponse(data, req, c.cfg, c.backend.RevalidatorMaker(req))
		if err != nil {
			log.Err(err).Msg("[dump] failed to create response")
			failed++
			continue
		}

		uniq := fmt.Sprintf("%d-%d", req.ShardKey(), req.Key())
		if uniq != entry.Unique {
			log.Info().Msgf("ENTRY: dom: %s, proj: %s, lng: %s, tags: %s", entry.Domain, entry.Project, entry.Language, string(bytes.Join(entry.Tags, []byte(","))))
			log.Info().Msgf("NEW_REQUEST: dom: %s, proj: %s, lng: %s, tags: %s", string(req.GetDomain()), string(req.GetProject()), string(req.GetLanguage()), bytes.Join(req.GetTags(), []byte(",")))
			panic(fmt.Sprintf("uniq (%s) != entry.Unique (%s) (keyBuf={now: %s, entry: %s})", uniq, entry.Unique, string(req.KeyBuf), string(entry.KeyBuf)))
		}

		c.Set(resp)
		success++
	}

	log.Info().Msgf("[dump] restored %d entries, errors: %d (elapsed: %s)", success, failed, time.Since(start))
	if failed > 0 {
		return fmt.Errorf("load completed with %d errors", failed)
	}
	return nil
}
