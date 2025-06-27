package advancedcache

import (
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/rs/zerolog/log"
)

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var middleware = &CacheMiddleware{}
	if err := middleware.UnmarshalCaddyfile(h.Dispenser); err != nil {
		log.Error().Err(err).Msg("[advanced-cache] failed to parse caddy config")
		return nil, err
	}
	return middleware, nil
}
