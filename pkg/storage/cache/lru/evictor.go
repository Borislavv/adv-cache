package lru

import (
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/utils"
	"time"
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

func (c *Storage) usedMem() int64 {
	return c.shardedMap.Mem()
}

// shouldEvict checks if current Weight usage has reached or exceeded the threshold.
func (c *Storage) shouldEvict() bool {
	return c.usedMem() >= c.memoryThreshold
}

// evictUntilWithinLimit repeatedly removes entries from the most loaded shard (tail of Storage)
// until Weight drops below threshold or no more can be evicted.
func (c *Storage) evictUntilWithinLimit() (items int, mem int64) {
	maxShards := float64(sharded.ShardCount)
	percentage := int(maxShards * 0.25)

	shardOffset := 0
	for c.shouldEvict() {
		shardOffset++
		if shardOffset >= percentage {
			shardOffset = 0
		}

		c.balancer.Rebalance()
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
			lru.Lock()
			el, ok := lru.NextUnlocked(offset)
			if !ok {
				lru.Unlock()
				break
			}

			key := el.Value().Key()
			shardKey := el.Value().ShardKey()
			lru.Unlock()

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
