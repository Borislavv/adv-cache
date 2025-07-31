package route

import (
	"encoding/json"
	"github.com/valyala/fasthttp"
)

const disableRoutePath = "/cache/off"

type DisableRoute struct{}

func NewDisableRoute() *DisableRoute {
	return &DisableRoute{}
}

func (c *DisableRoute) Handle(r *fasthttp.RequestCtx) error {
	DisableCache()
	r.SetStatusCode(fasthttp.StatusOK)
	_ = json.NewEncoder(r).Encode(onOffStatusResponse{Enabled: true, Message: "cache disabled"})
	return nil
}

func (c *DisableRoute) Paths() []string {
	return []string{disableRoutePath}
}

func (c *DisableRoute) IsEnabled() bool {
	return IsCacheEnabled()
}

func (c *DisableRoute) IsInternal() bool {
	return true
}
