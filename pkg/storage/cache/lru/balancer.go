package lru

import (
	"context"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/consts"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/list"
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/utils"
	"math/rand/v2"
	"sync/atomic"
	"time"
	"unsafe"
)

// ShardNode represents a single shard's Storage and accounting info.
// Each shard has its own Storage list and a pointer to its element in the balancer's memList.
type ShardNode struct {
	lruList     *list.List[*model.Response]     // Per-shard Storage list; less used responses at the back
	memListElem *list.Element[*ShardNode]       // Pointer to this node's position in Balance.memList
	shard       *sharded.Shard[*model.Response] // Reference to the actual shard (map + sync)
	len         int64                           // Number of items in the shard (atomically updated)
}

// Weight returns an approximate Weight usage of this ShardNode structure.
func (s *ShardNode) Weight() int64 {
	return int64(unsafe.Sizeof(*s)) + atomic.LoadInt64(&s.len)*consts.PtrBytesWeight
}

type Balancer interface {
	rebalance()
	Shards() [sharded.ShardCount]*ShardNode
	RandShardNode() *ShardNode
	Register(shard *sharded.Shard[*model.Response])
	Set(resp *model.Response) *ShardNode
	Update(existing *model.Response)
	Move(shardKey uint64, el *list.Element[*model.Response])
	Remove(shardKey uint64)
	MostLoadedSampled(offset int) (*ShardNode, bool)
}

// Balance maintains per-shard Storage lists and provides efficient selection of loaded shards for eviction.
// - memList orders shardNodes by usage (most loaded in front).
// - shards is a flat array for O(1) access by shard index.
// - shardedMap is the underlying data storage (map of all entries).
type Balance struct {
	ctx        context.Context
	shards     [sharded.ShardCount]*ShardNode // Shard index → *ShardNode
	memList    *list.List[*ShardNode]         // Doubly-linked list of shards, ordered by Memory usage (most loaded at front)
	shardedMap *sharded.Map[*model.Response]  // Actual underlying storage of entries
}

// NewBalancer creates a new Balance instance and initializes memList.
func NewBalancer(ctx context.Context, shardedMap *sharded.Map[*model.Response]) *Balance {
	return &Balance{
		ctx:        ctx,
		memList:    list.New[*ShardNode](), // Sorted mode for easier rebalancing
		shardedMap: shardedMap,
	}
}

func (b *Balance) RunRebalancer() {
	go func() {
		t := utils.NewTicker(b.ctx, time.Millisecond*500)
		for {
			select {
			case <-b.ctx.Done():
				return
			case <-t:
				// sort shardNodes by weight (freedMem)
				b.memList.Sort(list.DESC)
			}
		}
	}()
}

func (b *Balance) rebalance() {
	// sort shardNodes by weight (freedMem)
	b.memList.Sort(list.DESC)
}

func (b *Balance) Shards() [sharded.ShardCount]*ShardNode {
	return b.shards
}

// RandShardNode returns a random ShardNode for sampling (e.g., for background refreshers).
func (b *Balance) RandShardNode() *ShardNode {
	return b.shards[rand.Uint64N(sharded.ShardCount)]
}

// Register inserts a new ShardNode for a given shard, creates its Storage, and adds it to memList and shards array.
func (b *Balance) Register(shard *sharded.Shard[*model.Response]) {
	n := &ShardNode{
		shard:   shard,
		lruList: list.New[*model.Response](),
	}
	n.memListElem = b.memList.PushBack(n)
	b.shards[shard.ID()] = n
}

// Set inserts a response into the appropriate shard's Storage list and updates counters.
// Returns the affected ShardNode for further operations.
func (b *Balance) Set(resp *model.Response) *ShardNode {
	node := b.shards[resp.Request().ShardKey()]
	atomic.AddInt64(&node.len, 1)
	resp.SetLruListElement(node.lruList.PushFront(resp))
	return node
}

func (b *Balance) Update(existing *model.Response) {
	b.shards[existing.ShardKey()].lruList.MoveToFront(existing.LruListElement())
}

// Move moves an element to the front of the per-shard Storage list.
// Used for touch/Set operations to mark entries as most recently used.
func (b *Balance) Move(shardKey uint64, el *list.Element[*model.Response]) {
	b.shards[shardKey].lruList.MoveToFront(el)
}

// Remove releases an entry from the shardedMap and removes it from the per-shard Storage list.
// Also updates counters and rebalances memList if necessary.
// Returns (memory_freed, was_found).
func (b *Balance) Remove(shardKey uint64) {
	atomic.AddInt64(&b.shards[shardKey].len, -1)
}

// MostLoadedSampled returns the first non-empty shard node from the front of memList,
// optionally skipping a number of nodes by offset (for concurrent eviction fairness).
func (b *Balance) MostLoadedSampled(offset int) (*ShardNode, bool) {
	b.rebalance()
	el, ok := b.memList.Next(offset)
	if !ok {
		return nil, false
	}
	return el.Value(), ok
}
