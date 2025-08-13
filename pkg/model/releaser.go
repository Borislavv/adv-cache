package model

import "github.com/valyala/fasthttp"

type Releaser func(request *fasthttp.Request, response *fasthttp.Response)

var emptyReleaser Releaser = func(request *fasthttp.Request, response *fasthttp.Response) {}
