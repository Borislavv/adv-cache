package handler

import "net/http"

type RouteNotFound struct {
}

func NewRouteNotFound() *RouteNotFound {
	return &RouteNotFound{}
}

func (f *RouteNotFound) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status": 404,"error":"Not Found","message":"Route not found, check the URL is correct."}`))
}
