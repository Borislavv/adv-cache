package lru

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	"github.com/rs/zerolog/log"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	dumpFileName = "cache.dump"
	filePerm     = 0644
)

// DumpToDir writes all shards into a single binary file with length-prefixed records.
func (c *Storage) DumpToDir(ctx context.Context, dir string) error {
	from := time.Now()

	filename := filepath.Join(dir, dumpFileName)
	f, err := os.Create(filename)
	if err != nil {
		err = fmt.Errorf("create dump file error: %w", err)
		log.Err(err).Msg("[dump] " + err.Error())
		return err
	}
	defer func() { _ = f.Close() }()

	gz := gzip.NewWriter(f)
	defer func() { _ = gz.Close() }()

	bufWriter := bufio.NewWriterSize(gz, 8*1024*1024) // 8MB буфер

	errors := 0
	success := 0
	for shardID, node := range c.balancer.Shards() {
		node.shard.Walk(ctx, func(_ uint64, resp *model.Response) bool {
			select {
			case <-ctx.Done():
				return false
			default:
				if data, err := resp.MarshalBinary(); err != nil {
					log.Err(err).Msg("[dump] " + fmt.Errorf("marshal data to binary failed: %w", err).Error())
					errors++
					return true
				} else {
					// [shard_id:uint16][len:uint32][payload]
					if err = binary.Write(bufWriter, binary.LittleEndian, uint16(shardID)); err != nil {
						log.Err(err).Msg("[dump] write shard_id to binary failed")
						errors++
						return true
					}
					if err = binary.Write(bufWriter, binary.LittleEndian, uint32(len(data))); err != nil {
						log.Err(err).Msg("[dump] write payload length to binary failed")
						errors++
						return true
					}
					if _, err := bufWriter.Write(data); err != nil {
						log.Err(err).Msg("[dump] write payload to binary failed")
						errors++
						return true
					}
					success++
				}
			}
			return true
		}, true)
	}

	if err = bufWriter.Flush(); err != nil {
		err = fmt.Errorf("flush buffer failed: %w", err)
		log.Err(err).Msg("[dump] " + err.Error())
		return err
	}

	if err = gz.Close(); err != nil {
		err = fmt.Errorf("flush gzip buffer failed: %w", err)
		log.Err(err).Msg("[dump] " + err.Error())
		return err
	}

	log.Info().Msgf(
		"[dump] dump to: %s successfully written %d keys, errors %d (elapsed: %s)",
		filename, success, errors, time.Since(from).String(),
	)

	if errors > 0 {
		return fmt.Errorf(fmt.Sprintf("dump errors: %d", errors))
	}

	return nil
}

// LoadFromDir loads all data from the single dump file.
func (c *Storage) LoadFromDir(ctx context.Context, dir string) error {
	from := time.Now()

	filename := filepath.Join(dir, dumpFileName)
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open dump file: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("make a gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()

	bufReader := bufio.NewReaderSize(gz, 8*1024*1024) // 8mb

	errors := 0
	success := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			var shardID uint16
			if err = binary.Read(bufReader, binary.LittleEndian, &shardID); err != nil {
				if err == io.EOF {
					log.Info().Msgf("[dump] successfully loaded %d keys, errors: %d (elapsed: %s)", success, errors, time.Since(from).String())
					return nil
				}
				log.Err(err).Msgf("[dump] read shard_id: %s", err.Error())
				errors++
				continue
			}

			var dataLen uint32
			if err = binary.Read(bufReader, binary.LittleEndian, &dataLen); err != nil {
				log.Err(err).Msgf("[dump] read dataLen: %s", err.Error())
				errors++
				continue
			}

			data := make([]byte, dataLen)
			if _, err = io.ReadFull(bufReader, data); err != nil {
				log.Err(err).Msgf("[dump] read full payload: %s", err.Error())
				errors++
				continue
			}

			resp := new(model.Response).Init().Touch()
			if err = resp.UnmarshalBinary(data, c.backend.RevalidatorMaker); err != nil {
				log.Err(err).Msgf("[dump] unmarshal binary: %s", err.Error())
				errors++
				continue
			}

			c.Set(resp)
			success++
		}
	}
}
