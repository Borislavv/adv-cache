package route

import (
	"encoding/json"
	"net/http"
)

const enabledRoutePath = "/cache/on"

// clearStatusResponse is the JSON payload returned by On and Off handlers.
type onOffStatusResponse struct {
	Enabled bool   `json:"isCacheEnabled"`
	Message string `json:"message,omitempty"`
}

type EnableRoute struct{}

func NewEnableRoute() *EnableRoute {
	return &EnableRoute{}
}

// handleThroughProxy handles POST /adv-cache/on and enables the advanced cache, returning JSON.
func (c *EnableRoute) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	EnableCache()
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(onOffStatusResponse{Enabled: true, Message: "cache isCacheEnabled"})
	return nil
}

func (c *EnableRoute) Paths() []string {
	return []string{enabledRoutePath}
}

func (c *EnableRoute) IsEnabled() bool {
	return IsCacheEnabled()
}

func (c *EnableRoute) IsInternal() bool {
	return true
}
