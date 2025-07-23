package api

import (
	"encoding/json"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

// OnOffController provides endpoints to switch the advanced cache on and off.
type OnOffController struct{}

// NewOnOffController creates a new OnOffController instance.
func NewOnOffController() *OnOffController {
	return &OnOffController{}
}

// clearStatusResponse is the JSON payload returned by On and Off handlers.
type onOffStatusResponse struct {
	Enabled bool   `json:"enabled"`
	Message string `json:"message,omitempty"`
}

// On handles POST /adv-cache/on and enables the advanced cache, returning JSON.
func (c *OnOffController) On(ctx *fasthttp.RequestCtx) {
	enabled.Store(true)
	resp := onOffStatusResponse{Enabled: true, Message: "cache enabled"}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json; charset=utf-8")
	_ = json.NewEncoder(ctx).Encode(resp)
}

// Off handles POST /adv-cache/off and disables the advanced cache, returning JSON.
func (c *OnOffController) Off(ctx *fasthttp.RequestCtx) {
	enabled.Store(false)
	resp := onOffStatusResponse{Enabled: false, Message: "cache disabled"}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json; charset=utf-8")
	_ = json.NewEncoder(ctx).Encode(resp)
}

// AddRoute attaches the on/off routes to the given router.
func (c *OnOffController) AddRoute(r *router.Router) {
	r.GET("/cache/on", c.On)
	r.GET("/cache/off", c.Off)
}
