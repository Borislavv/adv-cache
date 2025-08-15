package model

// SetMapKeyForTests is really dangerous - must be used exclusively in tests.
func (e *Entry) SetMapKeyForTests(key uint64) *Entry {
	e.key = key
	return e
}
