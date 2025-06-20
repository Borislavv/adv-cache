package lru

import (
	"context"
	"math/rand/v2"
	"unsafe"

	"github.com/Borislavv/traefik-http-cache-plugin/pkg/model"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/list"
	sharded "github.com/Borislavv/traefik-http-cache-plugin/pkg/storage/map"
)

// ShardNode represents a single shard's Storage and accounting info.
// Each shard has its own Storage list and a pointer to its element in the balancer's memList.
type ShardNode struct {
	lruList     *list.List[*model.Response]     // Per-shard Storage list; less used responses at the back
	memListElem *list.Element[*ShardNode]       // Pointer to this node's position in Balance.memList
	shard       *sharded.Shard[*model.Response] // Reference to the actual shard (map + sync)
}

// Weight returns an approximate Weight usage of this ShardNode structure.
func (s *ShardNode) Weight() int64 {
	return int64(unsafe.Sizeof(*s))
}

type Balancer interface {
	Rebalance()
	Shards() [sharded.NumOfShards]*ShardNode
	RandShardNode() *ShardNode
	Register(shard *sharded.Shard[*model.Response])
	Set(resp *model.Response)
	Update(existing *model.Response)
	Move(shardKey uint64, el *list.Element[*model.Response])
	MostLoadedSampled(offset int) (*ShardNode, bool)
	FindVictim(shardKey uint64) (*model.Response, bool)
}

// Balance maintains per-shard Storage lists and provides efficient selection of loaded shards for eviction.
// - memList orders shardNodes by usage (most loaded in front).
// - shards is a flat array for O(1) access by shard index.
// - shardedMap is the underlying data storage (map of all entries).
type Balance struct {
	ctx        context.Context
	shards     [sharded.NumOfShards]*ShardNode // Shard index → *ShardNode
	memList    *list.List[*ShardNode]          // Doubly-linked list of shards, ordered by Memory usage (most loaded at front)
	shardedMap *sharded.Map[*model.Response]   // Actual underlying storage of entries
}

// NewBalancer creates a new Balance instance and initializes memList.
func NewBalancer(ctx context.Context, shardedMap *sharded.Map[*model.Response]) *Balance {
	return &Balance{
		ctx:        ctx,
		memList:    list.New[*ShardNode](), // Sorted mode for easier rebalancing
		shardedMap: shardedMap,
	}
}

func (b *Balance) Rebalance() {
	// sort shardNodes by weight (freedMem)
	b.memList.Sort(list.DESC)
}

func (b *Balance) Shards() [sharded.NumOfShards]*ShardNode {
	return b.shards
}

// RandShardNode returns a random ShardNode for sampling (e.g., for background refreshers).
func (b *Balance) RandShardNode() *ShardNode {
	return b.shards[rand.Uint64N(sharded.NumOfShards)]
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
func (b *Balance) Set(resp *model.Response) {
	resp.SetLruListElement(b.shards[resp.Request().ShardKey()].lruList.PushFront(resp))
}

func (b *Balance) Update(existing *model.Response) {
	b.shards[existing.ShardKey()].lruList.MoveToFront(existing.LruListElement())
}

// Move moves an element to the front of the per-shard Storage list.
// Used for touch/Set operations to mark entries as most recently used.
func (b *Balance) Move(shardKey uint64, el *list.Element[*model.Response]) {
	b.shards[shardKey].lruList.MoveToFront(el)
}

// MostLoadedSampled returns the first non-empty shard node from the front of memList,
// optionally skipping a number of nodes by offset (for concurrent eviction fairness).
func (b *Balance) MostLoadedSampled(offset int) (*ShardNode, bool) {
	el, ok := b.memList.Next(offset)
	if !ok {
		return nil, false
	}
	return el.Value(), ok
}

func (b *Balance) FindVictim(shardKey uint64) (*model.Response, bool) {
	shardKeyInt64 := int64(shardKey)
	if el := b.shards[shardKeyInt64].lruList.Back(); el != nil {
		return el.Value(), true
	}
	if int64(len(b.shards)) > shardKeyInt64+1 {
		if el := b.shards[shardKeyInt64+1].lruList.Back(); el != nil {
			return el.Value(), true
		}
	}
	if shardKeyInt64-1 > 0 {
		if el := b.shards[shardKeyInt64-1].lruList.Back(); el != nil {
			return el.Value(), true
		}
	}
	return nil, false
}
