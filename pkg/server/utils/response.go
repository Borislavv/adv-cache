package serverutils

import (
	"errors"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
)

var ErrWriteResponse = errors.New("error occurred while writing data into *fasthttp.RequestCtx")

func Write(b []byte, ctx *fasthttp.RequestCtx) (int, error) {
	n, err := ctx.Write(b)
	if err != nil {
		log.Error().Err(err).Msg("error while writing data into *fasthttp.RequestCtx")
		return 0, ErrWriteResponse
	}
	return n, nil
}

func WriteString(s string, ctx *fasthttp.RequestCtx) (int, error) {
	n, err := ctx.WriteString(s)
	if err != nil {
		log.Error().Err(err).Msg("error while writing data into *fasthttp.RequestCtx")
		return 0, ErrWriteResponse
	}
	return n, nil
}
