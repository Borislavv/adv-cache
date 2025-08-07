package responder

import (
	"github.com/Borislavv/advanced-cache/pkg/http/header"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/valyala/fasthttp"
)

func WriteFromResponse(ctx *fasthttp.RequestCtx, resp *fasthttp.Response, lastModified int64) error {
	// Set up headers
	resp.Header.All()(func(k []byte, v []byte) bool {
		ctx.Response.Header.AddBytesKV(k, v)
		return true
	})

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

func WriteFromEntry(ctx *fasthttp.RequestCtx, entry *model.Entry) error {
	_, _, queryHeaders, headers, body, status, releaser, err := entry.Payload()
	defer releaser(queryHeaders, headers)
	if err != nil {
		return err
	}

	// Set up headers
	for _, kv := range *headers {
		ctx.Response.Header.AddBytesKV(kv[0], kv[1])
	}

	// Set up Last-Modified header
	header.SetLastModifiedFastHttp(ctx, entry)

	// Set up status code
	ctx.SetStatusCode(status)

	// Write response body
	if _, err = ctx.Write(body); err != nil {
		return err
	}

	return nil
}
