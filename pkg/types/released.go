package types

type Released interface {
	Acquire() (isAcquired bool)
	Release()
	Remove()
}
