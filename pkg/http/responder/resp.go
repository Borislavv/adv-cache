package responder

import (
	"github.com/Borislavv/advanced-cache/pkg/http/header"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/valyala/fasthttp"
)

func FromResponse(ctx *fasthttp.RequestCtx, resp *fasthttp.Response, lastRefreshedAt int64) {
	// Set up headers
	resp.Header.CopyTo(&ctx.Response.Header)

	// Set up Last-Updated-At header
	header.SetLastUpdatedAtValueFastHttp(ctx, lastRefreshedAt)

	// Set up status code
	ctx.SetStatusCode(resp.StatusCode())

	// Set up body
	ctx.SetBody(resp.Body())
}

func FromEntry(ctx *fasthttp.RequestCtx, entry *model.Entry) error {
	req, resp, releaser, err := entry.Payload()
	defer releaser(req, resp)
	if err != nil {
		return err
	}
	FromResponse(ctx, resp, entry.RefreshedAt())
	return nil
}

func Unavailable(err error) []byte {
	return []byte(`{
	  "status": 503,
	  "error": "Service Unavailable",
	  "message": "` + err.Error() + `"
	}`)
}
