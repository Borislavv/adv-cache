package liveness

import (
	"github.com/fasthttp/router"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

var (
	successResponseBytes = []byte(`{
	  "status": 200,
      "message": "I'm fine :D'"
	}`)
	failedResponseBytes = []byte(`{
	  "status": 503,
      "message": "I'm tired :('"
	}`)
)

type Controller struct {
	probe Prober
}

func NewController(probe Prober) *Controller {
	return &Controller{probe: probe}
}

func (c *Controller) Probe(ctx *fasthttp.RequestCtx) {
	if c.probe.IsAlive() {
		ctx.SetStatusCode(fasthttp.StatusOK)
		if _, err := ctx.Write(successResponseBytes); err != nil {
			log.Error().Err(err).Msg("[probe-controller] failed to write success response into *fasthttp.RequestCtx")
		}
		return
	}

	ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
	if _, err := ctx.Write(failedResponseBytes); err != nil {
		log.Error().Err(err).Msg("[probe-controller] failed to write failed response into *fasthttp.RequestCtx")
	}
}

func (c *Controller) AddRoute(router *router.Router) {
	router.GET("/k8s/probe", c.Probe)
}
