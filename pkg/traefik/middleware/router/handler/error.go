package handler

import "net/http"

type RouteInternalError struct {
}

func NewRouteInternalError() *RouteInternalError {
	return &RouteInternalError{}
}

func (f *RouteInternalError) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusInternalServerError)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{
		"status": 500,
		"error":"Internal server error",
		"message": "Please contact support though Reddy: Star team."
	}`))
}
