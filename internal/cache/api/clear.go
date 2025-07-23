package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/Borislavv/advanced-cache/internal/cache/config"
	"sync"
	"time"

	"github.com/Borislavv/advanced-cache/pkg/storage"
	"github.com/fasthttp/router"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

type ClearController struct {
	db      storage.Storage
	cfg     *config.Config
	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewClearController(cfg *config.Config, db storage.Storage) *ClearController {
	return &ClearController{cfg: cfg, db: db}
}

type tokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expiresAt"`
}

type clearStatusResponse struct {
	Cleared bool   `json:"cleared,omitempty"`
	Error   string `json:"error,omitempty"`
}

// HandleClear is mounted at GET /cache/clear.
// Without ?token, returns a valid token (5min TTL).
// With ?token, validates, clears storage, logs, and returns status.
func (c *ClearController) HandleClear(ctx *fasthttp.RequestCtx) {
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
			_ = json.NewEncoder(ctx).Encode(tokenResponse{tok, exp.UnixMilli()})
			return
		}
		c.mu.Unlock()

		// generate new token
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			log.Error().Err(err).Msg("token generation failed")
			ctx.Error("internal error", fasthttp.StatusInternalServerError)
			return
		}
		tok := hex.EncodeToString(b)
		exp := now.Add(5 * time.Minute)

		c.mu.Lock()
		c.token = tok
		c.expires = exp
		c.mu.Unlock()

		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json")
		_ = json.NewEncoder(ctx).Encode(tokenResponse{tok, exp.UnixMilli()})
		return
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
		return
	}

	// clear and log
	c.db.Clear()

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
}

func (c *ClearController) AddRoute(r *router.Router) {
	r.GET("/cache/clear", c.HandleClear)
}
