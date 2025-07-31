package handler

import (
	"github.com/valyala/fasthttp"
	"net/http"
)

var contentType = []byte("application/json; charset=utf-8")

type RouteInternalError struct {
}

func NewRouteInternalError() *RouteInternalError {
	return &RouteInternalError{}
}

func (f *RouteInternalError) Handle(r *fasthttp.RequestCtx) {
	r.SetStatusCode(http.StatusInternalServerError)
	_, _ = r.Write([]byte(`{
		"status": 500,
		"error":"Internal server error",
		"message": "Please contact support though Reddy: Star team."
	}`))
}
