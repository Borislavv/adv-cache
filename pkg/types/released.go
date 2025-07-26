package types

type Released interface {
	Acquire() (isAcquired bool)
	Release() (freedBytes int64, finalized bool)
	Remove() (freedBytes int64, finalized bool)
	RefCount() int64
	IsDoomed() int64
}
