package route

import (
	"github.com/valyala/fasthttp"
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

func (c *K8sProbeRoute) Handle(r *fasthttp.RequestCtx) error {
	r.SetStatusCode(http.StatusOK)
	_, _ = r.Write(successResponseBytes)
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
