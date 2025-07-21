package model

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"sync/atomic"
)

type VersionPointer struct {
	version uint64
	*Entry
}

func NewVersionPointer(entry *Entry) *VersionPointer {
	return &VersionPointer{
		version: atomic.LoadUint64(&entry.version),
		Entry:   entry,
	}
}

func (v *VersionPointer) Acquire() bool {
	return v != nil && v.Entry.Acquire(v.version)
}

func (v *VersionPointer) Version() uint64 {
	return v.version
}

func (v *VersionPointer) ShouldBeRefreshed(cfg *config.Cache) bool {
	return v != nil && v.Entry.ShouldBeRefreshed(cfg)
}
