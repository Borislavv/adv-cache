package handler

import "net/http"

type UnavailableRoute struct{}

func NewUnavailableRoute() *UnavailableRoute {
	return &UnavailableRoute{}
}

func (c *UnavailableRoute) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusServiceUnavailable)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{
	  "status": 503,
	  "error": "Service unavailable",
	  "message": "Please try again later and contact support though Reddy: Star team."
	}`))
}
