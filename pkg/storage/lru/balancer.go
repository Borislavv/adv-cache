package lru

import (
	"context"
	"github.com/Borislavv/advanced-cache/pkg/list"
	"github.com/Borislavv/advanced-cache/pkg/model"
	sharded "github.com/Borislavv/advanced-cache/pkg/storage/map"
	"unsafe"
)

// ShardNode represents a single Shard's LRUStorage and accounting info.
// Each Shard has its own LRUStorage list and a pointer to its element in the balancer's memList.
type ShardNode struct {
	lruList     *list.List[*model.Entry]     // Per-Shard LRUStorage list; less used responses at the back
	memListElem *list.Element[*ShardNode]    // Pointer to this node's position in Balance.memList
	Shard       *sharded.Shard[*model.Entry] // Reference to the actual Shard (map + sync)
}

// Weight returns an approximate Weight usage of this ShardNode structure.
func (s *ShardNode) Weight() int64 {
	return s.Shard.Weight()
}

func (s *ShardNode) LruList() *list.List[*model.Entry] {
	return s.lruList
}

type Balancer interface {
	Rebalance()
	Mem() int64
	Register(shard *sharded.Shard[*model.Entry])
	Push(entry *model.Entry)
	Update(existing *model.Entry)
	Remove(shardKey uint64, el *list.Element[*model.Entry])
	MostLoaded(offset int) (*ShardNode, bool)
	FindVictim(shardKey uint64) (*model.Entry, bool)
}

// Balance maintains per-Shard LRUStorage lists and provides efficient selection of loaded shards for eviction.
// - memList orders shardNodes by usage (most loaded in front).
// - shards is a flat array for O(1) access by Shard index.
// - shardedMap is the underlying data storage (map of all entries).
type Balance struct {
	ctx        context.Context
	shards     [sharded.TotalShards]*ShardNode // Shard index → *ShardNode
	memList    *list.List[*ShardNode]          // Doubly-linked list of shards, ordered by Memory usage (most loaded at front)
	shardedMap *sharded.Map[*model.Entry]      // Actual underlying storage of entries
}

var ptrBytesSize uint64 = 8

func (b *Balance) Mem() int64 {
	mem := int64(uint64(unsafe.Sizeof(*b)) + (sharded.TotalShards * ptrBytesSize) + (uint64(b.memList.Len()) * ptrBytesSize))
	if shard := b.shards[0]; shard != nil {
		mem += int64(uint64(unsafe.Sizeof(*shard)) * sharded.TotalShards)
	}
	return mem
}

// NewBalancer creates a new Balance instance and initializes memList.
func NewBalancer(ctx context.Context, shardedMap *sharded.Map[*model.Entry]) *Balance {
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

// Register inserts a new ShardNode for a given Shard, creates its LRUStorage, and adds it to memList and shards array.
func (b *Balance) Register(shard *sharded.Shard[*model.Entry]) {
	n := &ShardNode{
		Shard:   shard,
		lruList: list.New[*model.Entry](),
	}
	n.memListElem = b.memList.PushBack(n)
	b.shards[shard.ID()] = n
}

// Push inserts a response into the appropriate Shard's LRUStorage list and updates counters.
// Returns the affected ShardNode for further operations.
func (b *Balance) Push(entry *model.Entry) {
	entry.SetLruListElement(b.shards[entry.ShardKey()].lruList.PushFront(entry))
}

func (b *Balance) Update(existing *model.Entry) {
	b.shards[existing.ShardKey()].lruList.MoveToFront(existing.LruListElement())
}

func (b *Balance) Remove(shardKey uint64, el *list.Element[*model.Entry]) {
	b.shards[shardKey].lruList.Remove(el)
}

// MostLoaded returns the first non-empty Shard node from the front of memList,
// optionally skipping a number of nodes by offset (for concurrent eviction fairness).
func (b *Balance) MostLoaded(offset int) (*ShardNode, bool) {
	el, ok := b.memList.Next(offset)
	if !ok {
		return nil, false
	}
	return el.Value(), ok
}

func (b *Balance) FindVictim(shardKey uint64) (*model.Entry, bool) {
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
