package lru

import (
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/utils"
	"time"
)

const evictionsPerSample = 8

var (
	_maxShards          = float64(sharded.ShardCount)
	fatShardsPercentage = int(_maxShards * 0.25)
)

// runEvictor launches multiple evictor goroutines for concurrent eviction.
func (c *Storage) runEvictor() {
	go c.evictor()
}

// evictor is the main background eviction loop for one worker.
// Each worker tries to bring Weight usage under the threshold by evicting from most loaded shards.
func (c *Storage) evictor() {
	t := utils.NewTicker(c.ctx, time.Second)
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t:
			items, freedMem := c.evictUntilWithinLimit()
			if c.cfg.IsDebugOn() && (items > 0 || freedMem > 0) {
				evictionStatCh <- evictionStat{items: items, freedMem: freedMem}
			}
		}
	}
}

// shouldEvict checks if current Weight usage has reached or exceeded the threshold.
func (c *Storage) shouldEvict() bool {
	return c.shardedMap.Mem() >= c.memoryThreshold
}

// evictUntilWithinLimit repeatedly removes entries from the most loaded shard (tail of Storage)
// until Weight drops below threshold or no more can be evicted.
func (c *Storage) evictUntilWithinLimit() (items int, mem int64) {
	shardOffset := 0
	for c.shouldEvict() {
		shardOffset++
		if shardOffset >= fatShardsPercentage {
			shardOffset = 0
		}

		shard, found := c.balancer.MostLoadedSampled(shardOffset)
		if !found {
			continue
		}

		lru := shard.lruList
		if lru.Len() == 0 {
			break
		}

		offset := 0
		evictions := 0
		for c.shouldEvict() {
			el, ok := lru.Next(offset)
			if !ok {
				break
			}

			if el.Value().IsDoomed() {
				offset++
				continue
			}

			key := el.Value().Key()
			shardKey := el.Value().ShardKey()

			freedMem, isHit := c.del(key, shardKey)
			if isHit {
				items++
				evictions++
				mem += freedMem
			}

			offset++
		}
	}
	return
}
