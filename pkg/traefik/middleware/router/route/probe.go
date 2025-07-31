package route

import (
	"net/http"
)

const k8sProbeRoutePath = "/k8s/probe"

var (
	successResponseBytes = []byte(`{
	  "status": 200,
      "message": "I'm fine :D'"
	}`)
)

type K8sProbeRoute struct {
}

func NewK8sProbeRoute() *K8sProbeRoute {
	return &K8sProbeRoute{}
}

func (c *K8sProbeRoute) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(successResponseBytes)
	return nil
}

func (c *K8sProbeRoute) Paths() []string {
	return []string{k8sProbeRoutePath}
}

func (c *K8sProbeRoute) IsEnabled() bool {
	return IsCacheEnabled()
}

func (c *K8sProbeRoute) IsInternal() bool {
	return true
}
