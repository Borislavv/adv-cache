package handler

import (
	"github.com/valyala/fasthttp"
	"net/http"
)

type UnavailableRoute struct{}

func NewUnavailableRoute() *UnavailableRoute {
	return &UnavailableRoute{}
}

func (c *UnavailableRoute) Handle(r *fasthttp.RequestCtx) {
	r.SetStatusCode(http.StatusServiceUnavailable)
	_, _ = r.Write([]byte(`{
	  "status": 503,
	  "error": "Service unavailable",
	  "message": "Please try again later and contact support though Reddy: Star team."
	}`))
}
