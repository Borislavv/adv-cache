package upstream

import "github.com/Borislavv/advanced-cache/pkg/config"

// Upstream defines the interface for a repository that provides SEO page data.
type Upstream interface {
	Fetch(
		rule *config.Rule, path []byte, query []byte, queryHeaders *[][2][]byte,
	) (
		status int, headers *[][2][]byte, body []byte, releaseFn func(), err error,
	)
}
