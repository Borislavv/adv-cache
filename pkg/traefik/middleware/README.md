1. Register your middleware in traefik/routes.yml
```
http:
  middlewares:
    cache:
      advancedCache:
        configPath: "advancedCache.cfg.yaml"

```
2. Register your middleware in pkg/server/middleware/middlewares.go
```
   	if config.AdvancedCache != nil {
		if middleware != nil {
			return nil, badConf
		}
		middleware = func(next http.Handler) (http.Handler, error) {
			return advancedcachemiddleware.New(ctx, next, config.AdvancedCache, middlewareName), nil
		}
	}
```
3. Copy and configure middleware in advancedCache.cfg.yaml (store it in the root traefik dir)