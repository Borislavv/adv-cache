package model

import "github.com/Borislavv/advanced-cache/pkg/config"

type Revalidator func(rule *config.Rule, path []byte, query []byte, queryHeaders [][2][]byte) (
	status int, headers [][2][]byte, body []byte, releaseFn func(), err error,
)
