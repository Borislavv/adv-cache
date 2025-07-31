package handler

import (
	"github.com/valyala/fasthttp"
	"net/http"
)

type RouteNotFound struct {
}

func NewRouteNotFound() *RouteNotFound {
	return &RouteNotFound{}
}

func (f *RouteNotFound) Handle(r *fasthttp.RequestCtx) {
	r.SetStatusCode(http.StatusNotFound)
	_, _ = r.Write([]byte(`{"status": 404,"error":"Not Found","message":"Route not found, check the URL is correct."}`))
}
