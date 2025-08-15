package model

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"sync"
)

var bytesBufferPool = &sync.Pool{New: func() any { return new(bytes.Buffer) }}

func (e *Entry) ToBytes() (data []byte, releaseFn func()) {
	var scratch8 [8]byte
	var scratch4 [4]byte

	buf := bytesBufferPool.Get().(*bytes.Buffer)
	releaseFn = func() {
		buf.Reset()
		bytesBufferPool.Put(buf)
	}

	// RulePath
	rulePath := e.Rule().PathBytes
	binary.LittleEndian.PutUint32(scratch4[:], uint32(len(rulePath)))
	buf.Write(scratch4[:])
	buf.Write(rulePath)

	// RuleKey
	binary.LittleEndian.PutUint64(scratch8[:], e.key)
	buf.Write(scratch8[:])

	// Shard
	binary.LittleEndian.PutUint64(scratch8[:], e.shard)
	buf.Write(scratch8[:])

	// Fingerprint
	buf.Write(e.fingerprint[:])

	// RefreshedAt
	binary.LittleEndian.PutUint64(scratch8[:], uint64(e.RefreshedAt()))
	buf.Write(scratch8[:])

	// Payload
	payload := e.payloadBytes()
	payloadLen := uint32(len(payload))
	binary.LittleEndian.PutUint32(scratch4[:], payloadLen)
	buf.Write(scratch4[:])
	if payloadLen > 0 {
		buf.Write(payload)
	}

	return buf.Bytes(), releaseFn
}

func EntryFromBytes(data []byte, cfg config.Config) (*Entry, error) {
	var offset int

	// RulePath
	rulePathLen := binary.LittleEndian.Uint32(data[offset:])
	offset += 4
	rulePath := data[offset : offset+int(rulePathLen)]
	offset += int(rulePathLen)

	rule := MatchRule(cfg, rulePath)
	if rule == nil {
		return nil, fmt.Errorf("rule not found for path: '%s'", string(rulePath))
	}

	// RuleKey
	key := binary.LittleEndian.Uint64(data[offset:])
	offset += 8

	// Shard
	shard := binary.LittleEndian.Uint64(data[offset:])
	offset += 8

	// Fingerprint
	var fp [16]byte
	copy(fp[:], data[offset:offset+16])
	offset += 16

	// RefreshedAt
	updatedAt := int64(binary.LittleEndian.Uint64(data[offset:]))
	offset += 8

	// Payload
	payloadLen := binary.LittleEndian.Uint32(data[offset:])
	offset += 4
	payload := data[offset : offset+int(payloadLen)]

	return newEntryFromField(key, shard, fp, payload, rule, updatedAt), nil
}
