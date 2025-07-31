package route

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/storage"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

const cacheClearPath = "/cache/clear"

type tokenResponse struct {
	Token     string `json:"token,omitempty"`
	Error     string `json:"error,omitempty"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

type clearStatusResponse struct {
	Cleared bool   `json:"cleared,omitempty"`
	Error   string `json:"error,omitempty"`
}

type ClearCacheRoute struct {
	mu      *sync.Mutex
	cfg     *config.Cache
	token   string
	expires time.Time
	storage storage.Storage
}

func NewClearRoute(cfg *config.Cache, storage storage.Storage) *ClearCacheRoute {
	return &ClearCacheRoute{
		mu:      new(sync.Mutex),
		cfg:     cfg,
		storage: storage,
	}
}

// Handle is mounted at GET /cache/clear.
// Without ?token, returns a valid token (5min TTL).
// With ?token, validates, clears storage, logs, and returns status.
func (m *ClearCacheRoute) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	now := time.Now()
	raw := r.URL.Query().Get("token")

	if raw == "" {
		// return or reuse token
		m.mu.Lock()
		if m.token != "" && now.Before(m.expires) {
			tok, exp := m.token, m.expires
			m.mu.Unlock()
			w.WriteHeader(fasthttp.StatusForbidden)

			_ = json.NewEncoder(w).Encode(tokenResponse{Token: tok, ExpiresAt: exp.UnixMilli()})
			return nil
		}
		m.mu.Unlock()

		// generate new token
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			log.Error().Err(err).Msg("[clear-controller] token generation failed")
			w.WriteHeader(fasthttp.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(tokenResponse{Error: err.Error()})
			return nil
		}
		tok := hex.EncodeToString(b)
		exp := now.Add(5 * time.Minute)

		m.mu.Lock()
		m.token = tok
		m.expires = exp
		m.mu.Unlock()

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(tokenResponse{Token: tok, ExpiresAt: exp.UnixMilli()})
		return nil
	}

	// validate provided token
	m.mu.Lock()
	valid := raw == m.token && now.Before(m.expires)
	m.token = ""
	m.expires = time.Time{}
	m.mu.Unlock()

	if !valid {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(clearStatusResponse{Error: "invalid or expired token"})
		return nil
	}

	// clear and log
	m.storage.Clear()

	logEvent := log.Info()
	if m.cfg.IsProd() {
		logEvent.
			Str("token", raw).
			Str("method", r.Method).
			Str("cacheClearPath", r.URL.Path).
			Str("user_agent", r.UserAgent()).
			Str("host", r.Host).
			Str("remote_addr", r.RemoteAddr).
			Time("time", time.Now())
	}
	logEvent.Msg("storage cleared")

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(clearStatusResponse{Cleared: true})

	return nil
}

func (m *ClearCacheRoute) Paths() []string {
	return []string{cacheClearPath}
}

func (m *ClearCacheRoute) IsEnabled() bool {
	return IsCacheEnabled()
}

func (m *ClearCacheRoute) IsInternal() bool {
	return true
}
