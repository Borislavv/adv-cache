package lru

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
	"github.com/rs/zerolog/log"
	"io"
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

// DumpToDir writes all shards into a single binary file with length-prefixed records.
func (c *Storage) DumpToDir(ctx context.Context, dir string) error {
	start := time.Now()
	filename := filepath.Join(dir, dumpFileName)

	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create dump file error: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewWriterLevel(f, gzip.BestSpeed)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	defer gz.Close()

	bufWriter := bufio.NewWriterSize(gz, 512*1024*1024) // 128MB

	var mu = &sync.Mutex{}
	var success, errors int32
	c.shardedMap.WalkShards(func(shardKey uint64, shard *sharded.Shard[*model.Response]) {
		shard.Walk(ctx, func(key uint64, resp *model.Response) bool {
			mu.Lock()
			defer mu.Unlock()

			if ctx.Err() != nil {
				log.Warn().Msg("[dump] context cancelled")
				return false
			}

			data, err := resp.MarshalBinary()
			if err != nil {
				log.Err(err).Msg("[dump] marshal error")
				atomic.AddInt32(&errors, 1)
				return true
			}

			header := [12]byte{}
			binary.LittleEndian.PutUint64(header[0:], shardKey)
			binary.LittleEndian.PutUint32(header[8:], uint32(len(data)))

			if _, err = bufWriter.Write(header[:]); err != nil {
				log.Err(err).Msg("[dump] write header error")
				atomic.AddInt32(&errors, 1)
				return true
			}
			if _, err = bufWriter.Write(data); err != nil {
				log.Err(err).Msg("[dump] write data error")
				atomic.AddInt32(&errors, 1)
				return true
			}
			atomic.AddInt32(&success, 1)
			return true
		}, true)
	})

	if err := bufWriter.Flush(); err != nil {
		return fmt.Errorf("flush buffer: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}

	log.Info().Msgf("[dump] written %d keys, errors: %d (elapsed: %s)", success, errors, time.Since(start))
	if errors > 0 {
		return fmt.Errorf("dump completed with %d errors", errors)
	}
	return nil
}

// LoadFromDir loads all data from the single dump file.
func (c *Storage) LoadFromDir(ctx context.Context, dir string) error {
	start := time.Now()
	filename := filepath.Join(dir, dumpFileName)

	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open dump file: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	bufReader := bufio.NewReaderSize(gz, 128*1024*1024) // 128MB

	var success, errors, total int

	header := make([]byte, 12) // 2 bytes shardID + 4 bytes dataLen

	for {
		if ctx.Err() != nil {
			log.Warn().Msg("[dump] context cancelled")
			return ctx.Err()
		}

		if _, err := io.ReadFull(bufReader, header); err != nil {
			if err == io.EOF {
				break
			}
			log.Err(err).Msg("[dump] read header error")
			errors++
			break
		}

		shardKey := binary.LittleEndian.Uint16(header[0:])
		dataLen := binary.LittleEndian.Uint32(header[8:])

		if dataLen == 0 || dataLen > 512*1024*1024 { // sanity check (max 512MB payload)
			log.Error().Uint16("shard", shardKey).Uint32("len", dataLen).Msg("[dump] invalid dataLen")
			errors++
			continue
		}

		data := make([]byte, dataLen)
		if _, err := io.ReadFull(bufReader, data); err != nil {
			log.Err(err).Uint16("shard", shardKey).Msg("[dump] read data error")
			errors++
			continue
		}

		resp := new(model.Response).Init().Touch()
		if err := resp.UnmarshalBinary(data, c.backend.RevalidatorMaker); err != nil {
			log.Err(err).Str("key", fmt.Sprintf("%d", resp.Key())).Msg("[dump] unmarshal failed")
			errors++
			continue
		}

		c.Set(resp)
		success++
		total++
	}

	log.Info().Msgf("[dump] loaded %d keys, errors: %d (elapsed: %s)", success, errors, time.Since(start))
	if errors > 0 {
		return fmt.Errorf("load completed with %d errors", errors)
	}
	return nil
}
