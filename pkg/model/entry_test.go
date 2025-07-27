package model

import (
	"bytes"
	"compress/gzip"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/stretchr/testify/require"
	"io"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func TestRefCountingNew(t *testing.T) {
	var (
		db   = make(map[int]*VersionPointer)
		mu   sync.Mutex
		done = make(chan struct{})
	)

	for idx := 0; idx < 150; idx++ {
		e := NewEntryFromField(0, 0, [16]byte{}, []byte(""), nil, nil, 0, 0)
		db[idx] = NewVersionPointer(e)
	}

	go func() {
		time.Sleep(5 * time.Second)
		close(done)
	}()

	// Readers
	for i := 0; i < 5; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					idx := rand.Intn(150)
					mu.Lock()
					te := db[idx]
					if te.Acquire() {
						mu.Unlock()
						payload := te.payload.Load()
						if payload == nil {
							panic("payload is nil")
						}
						te.Release(false)
					} else {
						mu.Unlock()
					}
				}
			}
		}()
	}

	// Writers
	for i := 0; i < 10; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					idx := rand.Intn(150)
					mu.Lock()
					old := db[idx]
					delete(db, idx)
					if old.Acquire() {
						old.Release(true)
					}
					e := NewEntryFromField(0, 0, [16]byte{}, []byte(""), nil, nil, 0, 0)
					db[idx] = NewVersionPointer(e)
					mu.Unlock()
				}
			}
		}()
	}

	time.Sleep(6 * time.Second)
}

func TestEntryPayloadRoundTrip(t *testing.T) {
	rule := &config.Rule{
		Gzip: config.Gzip{
			Enabled:   true,
			Threshold: 0, // Форсируем gzip даже для маленького тела
		},
	}

	// Исходные данные
	path := []byte("/test/path")
	query := []byte("?foo=bar&baz=qux")
	queryHeaders := [][2][]byte{
		{[]byte("X-Q-1"), []byte("v1")},
		{[]byte("X-Q-2"), []byte("v2")},
	}
	headers := [][2][]byte{
		{[]byte("Content-Type"), []byte("application/json")},
		{[]byte("Vary"), []byte("Accept-Encoding, Accept-Language")},
	}

	body := []byte(`{"foo":"bar","baz":"qux"}`)
	status := 200

	// === 1) Создаём Entry и упаковываем
	e := (&Entry{rule: rule}).Init()
	e.SetPayload(path, query, &queryHeaders, &headers, body, status)

	// === 2) Распаковываем
	path1, query1, queryHeaders1, respHeaders1, body1, status1, release, err := e.Payload()
	if err != nil {
		defer release(queryHeaders1, respHeaders1)
	}
	require.NoError(t, err)

	// === 3) Проверяем значения
	require.Equal(t, path, path1)
	require.Equal(t, query, query1)
	require.Equal(t, status, status1)
	require.Equal(t, body, body1)

	require.Equal(t, &queryHeaders, queryHeaders1)
	require.Equal(t, &headers, respHeaders1)

	// === 4) Повторно запаковываем, используя распакованные данные
	e2 := (&Entry{rule: rule}).Init()
	e2.SetPayload(path1, query1, queryHeaders1, respHeaders1, body1, status1)

	// === 5) И снова распаковываем
	path2, query2, queryHeaders2, respHeaders2, body2, status2, release2, err := e2.Payload()
	if err != nil {
		defer release2(queryHeaders2, respHeaders2)
	}
	require.NoError(t, err)

	require.Equal(t, path1, path2)
	require.Equal(t, query1, query2)
	require.Equal(t, status1, status2)
	require.Equal(t, body1, body2)
	require.Equal(t, queryHeaders1, queryHeaders2)
	require.Equal(t, respHeaders1, respHeaders2)

	// === 6) Проверим побайтово, что всё сохраняется и в сжатой форме
	if e2.IsCompressed() {
		// Распакуй и проверь вручную
		gr, err := gzip.NewReader(bytes.NewReader(e2.PayloadBytes()))
		require.NoError(t, err)
		defer gr.Close()

		raw, err := io.ReadAll(gr)
		require.NoError(t, err)

		gr1, err := gzip.NewReader(bytes.NewReader(e.PayloadBytes()))
		require.NoError(t, err)
		defer gr1.Close()

		raw1, err := io.ReadAll(gr1)
		require.NoError(t, err)

		require.Equal(t, raw1, raw, "compressed raw payloads must match")
	}
}
