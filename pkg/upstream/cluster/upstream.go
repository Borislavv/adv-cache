package cluster

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/valyala/fasthttp"
)

type Upstream interface {
	Fetch(rule *config.Rule, inCtx *fasthttp.RequestCtx, inReq *fasthttp.Request) (
		outReq *fasthttp.Request, outResp *fasthttp.Response,
		releaser func(*fasthttp.Request, *fasthttp.Response), err error,
	)
}
