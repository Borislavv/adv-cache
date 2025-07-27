package model

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
)

type VersionPointer struct {
	version uint32
	*Entry
}

func NewVersionPointer(entry *Entry) *VersionPointer {
	return &VersionPointer{
		version: entry.Version(),
		Entry:   entry,
	}
}

func (v *VersionPointer) Acquire() bool {
	return v != nil && v.Entry.Acquire(v.version)
}

func (v *VersionPointer) Version() uint32 {
	return v.version
}

func (v *VersionPointer) ShouldBeRefreshed(cfg *config.Cache) bool {
	return v != nil && v.Entry.ShouldBeRefreshed(cfg)
}
