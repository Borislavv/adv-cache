package upstream

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/valyala/fasthttp"
	"net/http"
)

// Upstream defines the interface for a repository that provides SEO page data.
type Upstream interface {
	FetchFH(
		ru *config.Rule,
		rq *fasthttp.RequestCtx,
	) (
		path, query []byte, qHeaders, rHeaders *[][2][]byte,
		status int, body []byte, releaseFn func(), err error,
	)
	FetchNH(
		ru *config.Rule,
		rq *http.Request,
	) (
		path, query []byte, qHeaders, rHeaders *[][2][]byte,
		status int, body []byte, releaseFn func(), err error,
	)
}
