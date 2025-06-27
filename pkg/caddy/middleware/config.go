package advancedcache

import (
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/rs/zerolog/log"
)

func (middleware *CacheMiddleware) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "config_path":
				if !d.Args(&middleware.ConfigPath) {
					return d.Errf("advancedcache config path expected by found in Caddyfile")
				}
			default:
				return d.Errf("unknown directive: %s", d.Val())
			}
		}
	}
	return nil
}

func (middleware *CacheMiddleware) loadConfig() (err error) {
	log.Info().Msgf("[advanced-cache] loading config by path %s", middleware.ConfigPath)
	if middleware.cfg, err = config.LoadConfig(middleware.ConfigPath); err != nil {
		return err
	}
	return nil
}
