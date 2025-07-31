package handler

import "net/http"

type RouteNotEnabled struct {
}

func NewRouteNotEnabled() *RouteNotEnabled {
	return &RouteNotEnabled{}
}

func (f *RouteNotEnabled) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusInternalServerError)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{
		"status": 403,
		"error": "Forbidden",
		"message": "Route is disabled"
	}`))
}
