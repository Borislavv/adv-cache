package model

import (
	"errors"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"unsafe"
)

var cacheRuleNotFoundError = errors.New("cache rule not found")

func (e *Entry) Rule() *config.Rule { return e.rule.Load() }

func MatchRule(cfg config.Config, path []byte) *config.Rule {
	if rule, ok := cfg.Rule(unsafe.String(unsafe.SliceData(path), len(path))); ok {
		return rule
	}
	return nil
}

func WetherAnErrIsTheRuleNotFound(err error) bool {
	return errors.Is(err, cacheRuleNotFoundError)
}
