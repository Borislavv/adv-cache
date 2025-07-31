package router

import (
	"github.com/Borislavv/advanced-cache/pkg/router/counter"
	"github.com/Borislavv/advanced-cache/pkg/router/handler"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"time"
	"unsafe"
)

type Router struct {
	routing     map[string]Route
	upstream    Upstream
	errored     InternalRoute
	notEnabled  InternalRoute
	unavailable InternalRoute
}

func NewRouter(upstream Upstream, routes ...Route) *Router {
	routing := make(map[string]Route, len(routes)*4)
	for _, route := range routes {
		for _, path := range route.Paths() {
			routing[path] = route
		}
	}
	return &Router{
		routing:     routing,
		upstream:    upstream,
		errored:     handler.NewRouteInternalError(),
		notEnabled:  handler.NewRouteNotEnabled(),
		unavailable: handler.NewUnavailableRoute(),
	}
}

func (router *Router) Handle(r *fasthttp.RequestCtx) {
	var from = time.Now()
	defer func() { counter.Duration.Add(time.Since(from).Nanoseconds()) }()
	counter.Total.Add(1)

	defer func() {
		if err := recover(); err != nil {
			counter.Panics.Add(1)
			log.Panic().Msgf("Recovered from panic: %v\n", err)
			router.unavailable.Handle(r)
			return
		}
	}()

	if route, ok := router.routing[unsafe.String(unsafe.SliceData(r.Path()), len(r.Path()))]; ok {
		if !route.IsEnabled() {
			router.notEnabled.Handle(r)
			return
		}

		if err := route.Handle(r); err != nil {
			counter.Errors.Add(1)
			if route.IsInternal() {
				return // error: respond error from internal route
			} // error: otherwise fallback to upstream
		} else {
			return // success: respond with route response
		}
	}

	if router.upstream.IsEnabled() {
		if err := router.upstream.Handle(r); err != nil {
			counter.Errors.Add(1)
			// error: respond that server is unavailable
		} else {
			return // success: respond with upstream response
		}
	}

	router.unavailable.Handle(r)
	return
}
