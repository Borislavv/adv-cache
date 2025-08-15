package model

import (
	"encoding/binary"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/bytes"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"time"
	"unsafe"
)

func (e *Entry) Weight() int64 { return int64(unsafe.Sizeof(*e)) + int64(cap(e.payloadBytes())) }

func (e *Entry) SwapPayloads(another *Entry) {
	another.payload.Store(e.payload.Swap(another.payload.Load()))
	e.touch()
}

func (e *Entry) IsSamePayload(another *Entry) bool {
	if curPayload := e.payloadBytes(); curPayload == nil {
		if another.payloadBytes() == nil {
			return true
		} else {
			return false
		}
	} else {
		if anotherPayload := another.payloadBytes(); anotherPayload == nil {
			return false
		} else {
			return bytes.IsBytesAreEquals(curPayload, anotherPayload)
		}
	}
}

func (e *Entry) payloadBytes() []byte {
	if ptr := e.payload.Load(); ptr == nil {
		return nil
	} else {
		return *ptr
	}
}

// SetPayload packs and gzip-compresses the entire payload: Path, Query, QueryHeaders, StatusCode, ResponseHeaders, Body.
func (e *Entry) SetPayload(req *fasthttp.Request, resp *fasthttp.Response) *Entry {
	path := req.URI().Path()
	query := req.URI().QueryString()
	body := resp.Body()

	// === 1) Calculate total size ===
	total := 0
	total += 4 + len(path)
	total += 4 + len(query)
	total += 4
	req.Header.All()(func(k []byte, v []byte) bool {
		total += 4 + len(k) + 4 + len(v)
		return true
	})
	total += 4
	total += 4
	resp.Header.All()(func(k []byte, v []byte) bool {
		total += 4 + len(k) + 4 + 4 + len(v)
		return true
	})
	total += 4 + len(body)

	// === 2) Allocate ===
	payloadBuf := make([]byte, 0, total)
	offset := 0

	var scratch [4]byte

	// === 3) Write ===

	// Path
	binary.LittleEndian.PutUint32(scratch[:], uint32(len(path)))
	payloadBuf = append(payloadBuf, scratch[:]...)
	payloadBuf = append(payloadBuf, path...)
	offset += len(path)

	// Query
	binary.LittleEndian.PutUint32(scratch[:], uint32(len(query)))
	payloadBuf = append(payloadBuf, scratch[:]...)
	payloadBuf = append(payloadBuf, query...)
	offset += len(query)

	// QueryHeaders
	binary.LittleEndian.PutUint32(scratch[:], uint32(req.Header.Len()))
	payloadBuf = append(payloadBuf, scratch[:]...)
	req.Header.All()(func(k []byte, v []byte) bool {
		binary.LittleEndian.PutUint32(scratch[:], uint32(len(k)))
		payloadBuf = append(payloadBuf, scratch[:]...)
		payloadBuf = append(payloadBuf, k...)
		offset += len(k)

		binary.LittleEndian.PutUint32(scratch[:], uint32(len(v)))
		payloadBuf = append(payloadBuf, scratch[:]...)
		payloadBuf = append(payloadBuf, v...)
		offset += len(v)
		return true
	})

	// StatusCode
	binary.LittleEndian.PutUint32(scratch[:], uint32(resp.StatusCode()))
	payloadBuf = append(payloadBuf, scratch[:]...)

	// ResponseHeaders
	binary.LittleEndian.PutUint32(scratch[:], uint32(resp.Header.Len()))
	payloadBuf = append(payloadBuf, scratch[:]...)
	resp.Header.All()(func(k []byte, v []byte) bool {
		binary.LittleEndian.PutUint32(scratch[:], uint32(len(k)))
		payloadBuf = append(payloadBuf, scratch[:]...)
		payloadBuf = append(payloadBuf, k...)
		offset += len(k)

		binary.LittleEndian.PutUint32(scratch[:], uint32(1))
		payloadBuf = append(payloadBuf, scratch[:]...)
		binary.LittleEndian.PutUint32(scratch[:], uint32(len(v)))
		payloadBuf = append(payloadBuf, scratch[:]...)
		payloadBuf = append(payloadBuf, v...)
		offset += len(v)
		return true
	})

	// Body
	binary.LittleEndian.PutUint32(scratch[:], uint32(len(body)))
	payloadBuf = append(payloadBuf, scratch[:]...)
	payloadBuf = append(payloadBuf, body...)
	offset += len(body)

	// === 5) Store raw ===
	payloadBuf = payloadBuf[:]
	e.payload.Store(&payloadBuf)

	// Tell everyone that entry already updated
	e.touch()

	return e
}

var ErrPayloadIsEmpty = fmt.Errorf("payload is empty")

// Payload decompresses the entire payload and unpacks it into fields.
func (e *Entry) Payload() (req *fasthttp.Request, resp *fasthttp.Response, releaser Releaser, err error) {
	payload := e.payloadBytes()
	if len(payload) == 0 {
		return nil, nil, emptyReleaser, ErrPayloadIsEmpty
	}

	req = fasthttp.AcquireRequest()
	resp = fasthttp.AcquireResponse()

	offset := 0

	// --- Path
	pathLen := binary.LittleEndian.Uint32(payload[offset:])
	offset += 4
	req.URI().SetPathBytes(payload[offset : offset+int(pathLen)])
	offset += int(pathLen)

	// --- Query
	queryLen := binary.LittleEndian.Uint32(payload[offset:])
	offset += 4
	req.URI().SetQueryStringBytes(payload[offset : offset+int(queryLen)])
	offset += int(queryLen)

	// --- QueryHeaders
	numQueryHeaders := binary.LittleEndian.Uint32(payload[offset:])
	offset += 4
	for i := 0; i < int(numQueryHeaders); i++ {
		keyLen := binary.LittleEndian.Uint32(payload[offset:])
		offset += 4
		k := payload[offset : offset+int(keyLen)]
		offset += int(keyLen)

		valueLen := binary.LittleEndian.Uint32(payload[offset:])
		offset += 4
		v := payload[offset : offset+int(valueLen)]
		offset += int(valueLen)

		req.Header.AddBytesKV(k, v)
	}

	// --- StatusCode
	resp.Header.SetStatusCode(int(binary.LittleEndian.Uint32(payload[offset:])))
	offset += 4

	// --- Response Headers
	numHeaders := binary.LittleEndian.Uint32(payload[offset:])
	offset += 4
	for i := 0; i < int(numHeaders); i++ {
		keyLen := binary.LittleEndian.Uint32(payload[offset:])
		offset += 4
		key := payload[offset : offset+int(keyLen)]
		offset += int(keyLen)

		numVals := binary.LittleEndian.Uint32(payload[offset:])
		offset += 4
		for v := 0; v < int(numVals); v++ {
			valueLen := binary.LittleEndian.Uint32(payload[offset:])
			offset += 4
			val := payload[offset : offset+int(valueLen)]
			offset += int(valueLen)

			resp.Header.AddBytesKV(key, val)
		}
	}

	// --- Body
	offset += 4
	resp.SetBodyRaw(payload[offset:])

	releaser = func(request *fasthttp.Request, response *fasthttp.Response) {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
	}

	return
}

func (e *Entry) DumpPayload() {
	req, resp, releaser, err := e.Payload()
	defer releaser(req, resp)
	if err != nil {
		log.Error().Err(err).Msg("[dump] failed to unpack payload")
		return
	}

	fmt.Printf("\n========== DUMP PAYLOAD ==========\n")
	fmt.Printf("RuleKey:          %d\n", e.key)
	fmt.Printf("Shard:        %d\n", e.shard)
	fmt.Printf("UpdatedAt:	 %s\n", time.Unix(0, e.UpdatedAt()).Format(time.RFC3339Nano))
	fmt.Printf("----------------------------------\n")

	fmt.Printf("Path:   	   %q\n", string(req.URI().Path()))
	fmt.Printf("Query:  	   %q\n", string(req.URI().QueryString()))
	fmt.Printf("StatusCode: %d\n", resp.StatusCode())

	fmt.Printf("\nQuery Headers:\n")
	if req.Header.Len() == 0 {
		fmt.Println("  (none)")
	} else {
		req.Header.All()(func(k []byte, v []byte) bool {
			fmt.Printf("  - %q : %q\n", k, v)
			return true
		})
	}

	fmt.Printf("\nResponse Headers:\n")
	if resp.Header.Len() == 0 {
		fmt.Println("  (none)")
	} else {
		resp.Header.All()(func(k []byte, v []byte) bool {
			fmt.Printf("  - %q : %q\n", k, v)
			return true
		})
	}

	fmt.Printf("\nBody (%d bytes):\n", len(resp.Body()))
	if len(resp.Body()) > 0 {
		const maxLen = 500
		if len(resp.Body()) > maxLen {
			fmt.Printf("  %q ... [truncated, total %d bytes]\n", resp.Body()[:maxLen], len(resp.Body()))
		} else {
			fmt.Printf("  %q\n", resp.Body())
		}
	}

	fmt.Println("==================================")
}
