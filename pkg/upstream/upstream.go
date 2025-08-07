package upstream

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/valyala/fasthttp"
)

// Upstream defines the interface for a repository that provides SEO page data.
type Upstream interface {
	Fetch(rule *config.Rule, ctx *fasthttp.RequestCtx) (
		request *fasthttp.Request, response *fasthttp.Response,
		releaser func(*fasthttp.Request, *fasthttp.Response), err error,
	)
}
