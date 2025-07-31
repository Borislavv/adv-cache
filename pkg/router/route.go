package router

import (
	"github.com/valyala/fasthttp"
)

type Upstream = Route

type Route interface {
	IsEnabled() bool
	IsInternal() bool
	Paths() []string
	Handle(r *fasthttp.RequestCtx) error
}

type InternalRoute interface {
	Handle(r *fasthttp.RequestCtx)
}
