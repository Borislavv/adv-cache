package upstream

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/valyala/fasthttp"
)

// Upstream defines the interface for external backends.
// Note: you need provide just one argument of inCtx or inReq.
type Upstream interface {
	Fetch(rule *config.Rule, inCtx *fasthttp.RequestCtx, inReq *fasthttp.Request) (
		outReq *fasthttp.Request, outResp *fasthttp.Response, releaser func(*fasthttp.Request, *fasthttp.Response), err error,
	)
}
