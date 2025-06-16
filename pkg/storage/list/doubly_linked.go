package list

import (
	synced "github.com/Borislavv/traefik-http-cache-plugin/pkg/sync"
	"github.com/Borislavv/traefik-http-cache-plugin/pkg/types"
	"sync"
	"unsafe"
)

// Element is a node in the doubly linked list that holds a value of type T.
// Never touch fields directly outside of List methods.
type Element[T types.Sized] struct {
	next, prev *Element[T]
	list       *List[T]
	value      T
}

func (e *Element[T]) Prev() *Element[T] {
	return e.prev
}
func (e *Element[T]) Next() *Element[T] {
	return e.next
}

// List returns the whole list if this element.
func (e *Element[T]) List() *List[T] {
	return e.list
}

// Value returns the value. NOT thread-safe! Only use inside locked section or single-threaded use!
func (e *Element[T]) Value() T {
	return e.value
}

func (e *Element[T]) Weight() int64 {
	return int64(unsafe.Sizeof(*e))
}

// List is a generic doubly linked list with optional thread safety.
type List[T types.Sized] struct {
	len      int
	mu       *sync.Mutex
	root     *Element[T]
	elemPool *synced.BatchPool[*Element[T]]
}

// New creates a new list. If isThreadSafe is true, all ops are guarded by a mutex.
func New[T types.Sized]() *List[T] {
	l := &List[T]{
		mu: &sync.Mutex{},
		elemPool: synced.NewBatchPool[*Element[T]](synced.PreallocateBatchSize, func() *Element[T] {
			return &Element[T]{}
		}),
	}
	l.init()
	return l
}

func (l *List[T]) init() *List[T] {
	root := l.elemPool.Get()
	*root = Element[T]{}
	l.root = root
	l.root.next = l.root
	l.root.prev = l.root
	l.len = 0
	return l
}

// Len returns the list length (O(1)). Thread-safe if guarded.
func (l *List[T]) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.len
}

func (l *List[T]) insert(e, at *Element[T]) *Element[T] {
	e.prev = at
	e.next = at.next
	at.next.prev = e
	at.next = e
	e.list = l
	l.len++
	return e
}

func (l *List[T]) insertValue(v T, at *Element[T]) *Element[T] {
	el := l.elemPool.Get()
	*el = Element[T]{value: v}
	return l.insert(el, at)
}

func (l *List[T]) remove(e *Element[T]) T {
	e.prev.next = e.next
	e.next.prev = e.prev
	val := e.value
	e.next = nil
	e.prev = nil
	e.list = nil
	l.elemPool.Put(e)
	l.len--
	return val
}

// Remove removes e from l and returns its value. Thread-safe.
func (l *List[T]) Remove(e *Element[T]) T {
	l.mu.Lock()
	defer l.mu.Unlock()

	if e == nil || e.list != l {
		var zero T
		return zero
	}
	return l.remove(e)
}

// PushFront inserts v at the front and returns new element. Thread-safe if guarded.
func (l *List[T]) PushFront(v T) *Element[T] {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.insertValue(v, l.root)
}

// PushBack inserts v at the back and returns new element. Thread-safe if guarded.
func (l *List[T]) PushBack(v T) *Element[T] {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.insertValue(v, l.root.prev)
}

// MoveToFront moves e to the front of the list without removing it from memory or touching the pool.
func (l *List[T]) MoveToFront(e *Element[T]) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if e == nil || e.list != l || e == l.root.next {
		return
	}

	// Detach e
	e.prev.next = e.next
	e.next.prev = e.prev

	// Move right after root (to front)
	e.prev = l.root
	e.next = l.root.next
	l.root.next.prev = e
	l.root.next = e
}

// swapElementsUnlocked moves a and b (nodes, not just values) in the list. Thread-safe if guarded.
func (l *List[T]) swapElementsUnlocked(a, b *Element[T]) {
	if a == nil || b == nil || a.list != l || b.list != l || a == b {
		return
	}

	// Actually swap elements, not values, for safety.
	// Remove both (in either order), then re-insert each at the other's old position.
	aPrev, aNext := a.prev, a.next
	bPrev, bNext := b.prev, b.next

	// Remove both from the list
	a.prev.next = a.next
	a.next.prev = a.prev
	b.prev.next = b.next
	b.next.prev = b.prev

	// Insert a at b's original position
	a.prev = bPrev
	a.next = bNext
	bPrev.next = a
	bNext.prev = a

	// Insert b at a's original position
	b.prev = aPrev
	b.next = aNext
	aPrev.next = b
	aNext.prev = b
}

func (l *List[T]) Lock() {
	l.mu.Lock()
}

func (l *List[T]) Unlock() {
	l.mu.Unlock()
}

// Next returns the element at the given offset from the front (0-based).
// Returns (nil, false) if offset is out of bounds.
func (l *List[T]) Next(offset int) (*Element[T], bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if offset < 0 || offset >= l.len {
		return nil, false
	}

	e := l.root.next
	for i := 0; i < offset; i++ {
		e = e.next
	}
	return e, true
}

// PrevUnlocked returns the element at the given offset from the back (0-based).
// Returns (nil, false) if offset is out of bounds.
func (l *List[T]) PrevUnlocked(offset int) (*Element[T], bool) {
	if offset < 0 || offset >= l.len {
		return nil, false
	}

	e := l.root.prev
	for i := 0; i < offset; i++ {
		e = e.prev
	}
	return e, true
}

// Walk executes fn for each element in order (under lock if guarded).
func (l *List[T]) Walk(dir Direction, fn func(l *List[T], el *Element[T]) (shouldContinue bool)) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch dir {
	case FromFront:
		e, n := l.root.next, l.len
		for {
			if n > 0 && e != nil {
				break
			}

			n, e = n-1, e.next
		}
		for e, n := l.root.next, l.len; n > 0 && e != nil; n, e = n-1, e.next {
			if !fn(l, e) {
				return
			}
		}
	case FromBack:
		for e, n := l.root.prev, l.len; n > 0 && e != nil; n, e = n-1, e.prev {
			if !fn(l, e) {
				return
			}
		}
	default:
		panic("unknown walk direction")
	}
}

// AdjustPositionIfNeeded repositions the element by Weight() in DESC order.
func (l *List[T]) AdjustPositionIfNeeded(e *Element[T]) {
	l.mu.Lock()
	defer l.mu.Unlock()

	w := e.Value().Weight()

	// Try moving up (toward head)
	for {
		prev := e.prev
		if prev == l.root || w <= prev.Value().Weight() {
			break
		}
		l.swapElementsUnlocked(prev, e)
		// после swap, e сместился на одну позицию назад → обновим указатель
		e = prev
	}

	// Try moving down (toward tail)
	for {
		next := e.next
		if next == l.root || w >= next.Value().Weight() {
			break
		}
		l.swapElementsUnlocked(e, next)
		// после swap, e сместился на одну позицию вперёд → обновим указатель
		e = next
	}
}

func (l *List[T]) Sort(ord Order) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.len < 2 {
		return
	}

	// Отвязка списка от корня (делаем из циклического — линейный)
	head := l.root.next
	l.root.prev.next = nil
	head.prev = nil

	// Рекурсивная сортировка
	sorted := l.mergeSort(head, l.len, ord)

	// Перестраиваем указатели prev и замыкаем список обратно
	l.root.next = sorted
	sorted.prev = l.root

	curr := sorted
	for curr.next != nil {
		curr.next.prev = curr
		curr = curr.next
	}
	curr.next = l.root
	l.root.prev = curr
}
func (l *List[T]) mergeSort(head *Element[T], n int, ord Order) *Element[T] {
	if head == nil {
		return nil
	}

	if n <= 1 {
		head.next = nil
		return head
	}

	mid := n / 2
	right := l.split(head, mid)

	left := l.mergeSort(head, mid, ord)
	right = l.mergeSort(right, n-mid, ord)

	return l.merge(left, right, ord)
}

func (l *List[T]) split(head *Element[T], count int) *Element[T] {
	curr := head
	for i := 1; i < count && curr != nil; i++ {
		curr = curr.next
	}

	if curr == nil {
		return nil
	}

	right := curr.next
	curr.next = nil
	if right != nil {
		right.prev = nil
	}
	return right
}

func (l *List[T]) merge(a, b *Element[T], ord Order) *Element[T] {
	var head, tail *Element[T]

	less := func(wa, wb int64) bool {
		if ord == ASC {
			return wa <= wb
		}
		return wa > wb
	}

	for a != nil && b != nil {
		if less(a.value.Weight(), b.value.Weight()) {
			if tail == nil {
				head = a
			} else {
				tail.next = a
			}
			a.prev = tail
			tail = a
			a = a.next
		} else {
			if tail == nil {
				head = b
			} else {
				tail.next = b
			}
			b.prev = tail
			tail = b
			b = b.next
		}
	}

	// Присоединить оставшиеся элементы
	rest := a
	if b != nil {
		rest = b
	}
	for rest != nil {
		tail.next = rest
		rest.prev = tail
		tail = rest
		rest = rest.next
	}

	return head
}
