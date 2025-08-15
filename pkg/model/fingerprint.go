package model

import "crypto/subtle"

func (e *Entry) Fingerprint() [16]byte { return e.fingerprint }
func (e *Entry) IsSameFingerprint(another [16]byte) bool {
	return subtle.ConstantTimeCompare(e.fingerprint[:], another[:]) == 1
}
