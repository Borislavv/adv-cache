package route

import (
	"encoding/json"
	"net/http"
)

const disableRoutePath = "/cache/off"

type DisableRoute struct{}

func NewDisableRoute() *DisableRoute {
	return &DisableRoute{}
}

func (c *DisableRoute) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	DisableCache()
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(onOffStatusResponse{Enabled: false, Message: "cache disabled"})
	return nil
}

func (c *DisableRoute) Paths() []string {
	return []string{disableRoutePath}
}

func (c *DisableRoute) IsEnabled() bool {
	return IsCacheEnabled()
}

func (c *DisableRoute) IsInternal() bool {
	return true
}
