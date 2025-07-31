package route

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/storage"
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
func (c *ClearCacheRoute) Handle(ctx *fasthttp.RequestCtx) error {
	now := time.Now()
	raw := string(ctx.QueryArgs().Peek("token"))

	if raw == "" {
		// return or reuse token
		c.mu.Lock()
		if c.token != "" && now.Before(c.expires) {
			tok, exp := c.token, c.expires
			c.mu.Unlock()
			ctx.SetStatusCode(fasthttp.StatusOK)
			ctx.SetContentType("application/json")
			_ = json.NewEncoder(ctx).Encode(tokenResponse{Token: tok, ExpiresAt: exp.UnixMilli()})
			return nil
		}
		c.mu.Unlock()

		// generate new token
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			log.Error().Err(err).Msg("token generation failed")
			ctx.Error("internal error", fasthttp.StatusInternalServerError)
			return nil
		}
		tok := hex.EncodeToString(b)
		exp := now.Add(5 * time.Minute)

		c.mu.Lock()
		c.token = tok
		c.expires = exp
		c.mu.Unlock()

		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json")
		_ = json.NewEncoder(ctx).Encode(tokenResponse{Token: tok, ExpiresAt: exp.UnixMilli()})
		return nil
	}

	// validate provided token
	c.mu.Lock()
	valid := raw == c.token && now.Before(c.expires)
	c.token = ""
	c.expires = time.Time{}
	c.mu.Unlock()

	if !valid {
		ctx.SetStatusCode(fasthttp.StatusForbidden)
		ctx.SetContentType("application/json")
		_ = json.NewEncoder(ctx).Encode(clearStatusResponse{Error: "invalid or expired token"})
		return nil
	}

	// clear and log
	c.storage.Clear()

	logEvent := log.Info()
	if c.cfg.IsProd() {
		logEvent.
			Str("token", raw).
			Str("ip", ctx.RemoteAddr().String()).
			Str("method", string(ctx.Method())).
			Str("path", string(ctx.Path())).
			Str("user_agent", string(ctx.UserAgent())).
			Time("time", time.Now())
	}
	logEvent.Msg("storage cleared")

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	_ = json.NewEncoder(ctx).Encode(clearStatusResponse{Cleared: true})

	return nil
}

func (c *ClearCacheRoute) Paths() []string {
	return []string{cacheClearPath}
}

func (c *ClearCacheRoute) IsEnabled() bool {
	return IsCacheEnabled()
}

func (c *ClearCacheRoute) IsInternal() bool {
	return true
}
