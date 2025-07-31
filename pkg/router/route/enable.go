package route

import (
	"encoding/json"
	"github.com/valyala/fasthttp"
)

const enabledRoutePath = "/cache/on"

// clearStatusResponse is the JSON payload returned by On and Off handlers.
type onOffStatusResponse struct {
	Enabled bool   `json:"isCacheEnabled"`
	Message string `json:"message,omitempty"`
}

type EnableRoute struct{}

func NewEnableRoute() *EnableRoute {
	return &EnableRoute{}
}

func (c *EnableRoute) Handle(r *fasthttp.RequestCtx) error {
	EnableCache()
	r.SetStatusCode(fasthttp.StatusOK)
	_ = json.NewEncoder(r).Encode(onOffStatusResponse{Enabled: true, Message: "cache enabled"})
	return nil
}

func (c *EnableRoute) Paths() []string {
	return []string{enabledRoutePath}
}

func (c *EnableRoute) IsEnabled() bool {
	return IsCacheEnabled()
}

func (c *EnableRoute) IsInternal() bool {
	return true
}
