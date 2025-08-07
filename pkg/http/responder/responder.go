package responder

import (
	"github.com/Borislavv/advanced-cache/pkg/http/header"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/valyala/fasthttp"
)

func WriteFromResponse(ctx *fasthttp.RequestCtx, resp *fasthttp.Response, lastModified int64) error {
	return write(ctx, resp, lastModified)
}

func WriteFromEntry(ctx *fasthttp.RequestCtx, entry *model.Entry) error {
	req, resp, releaser, err := entry.Payload()
	defer releaser(req, resp)
	if err != nil {
		return err
	}
	return write(ctx, resp, entry.UpdateAt())
}

func write(ctx *fasthttp.RequestCtx, resp *fasthttp.Response, lastModified int64) error {
	// Set up headers
	resp.Header.CopyTo(&ctx.Response.Header)

	// Set up Last-Modified header
	header.SetLastModifiedValueFastHttp(ctx, lastModified)

	// Set up status code
	ctx.SetStatusCode(resp.StatusCode())

	// Write response body
	if _, err := ctx.Write(resp.Body()); err != nil {
		return err
	}

	return nil
}
