package handler

import (
	"github.com/valyala/fasthttp"
	"net/http"
)

type RouteNotEnabled struct {
}

func NewRouteNotEnabled() *RouteNotEnabled {
	return &RouteNotEnabled{}
}

func (f *RouteNotEnabled) Handle(r *fasthttp.RequestCtx) {
	r.SetStatusCode(http.StatusNotAcceptable)
	_, _ = r.Write([]byte(`{
		"status": 403,
		"error": "Forbidden",
		"message": "Route is disabled"
	}`))
}
