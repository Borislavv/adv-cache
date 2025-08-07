package fasthttpconfig

import (
	"time"
)

type Configurator interface {
	GetHttpServerName() string
	GetHttpServerPort() string
	GetHttpServerShutDownTimeout() time.Duration
	GetHttpServerRequestTimeout() time.Duration
}

type Server struct {
	// ServerName is a name of the shared server.
	ServerName string `envconfig:"FASTHTTP_SERVER_NAME" mapstructure:"FASTHTTP_SERVER_NAME" default:"@BorislavGlazunov"`
	// ServerPort is a port for shared server (endpoints like a /probe for k8s).
	ServerPort string `envconfig:"FASTHTTP_SERVER_PORT" mapstructure:"FASTHTTP_SERVER_PORT" default:":8020"`
	// ServerShutDownTimeout is a duration value before the server will be closed forcefully.
	ServerShutDownTimeout time.Duration `envconfig:"FASTHTTP_SERVER_SHUTDOWN_TIMEOUT" mapstructure:"FASTHTTP_SERVER_SHUTDOWN_TIMEOUT" default:"5s"`
	// ServerRequestTimeout is a timeout value for close request forcefully.
	ServerRequestTimeout time.Duration `envconfig:"FASTHTTP_SERVER_REQUEST_TIMEOUT" mapstructure:"FASTHTTP_SERVER_REQUEST_TIMEOUT" default:"1m"`
}

func (c Server) GetHttpServerName() string {
	return c.ServerName
}

func (c Server) GetHttpServerPort() string {
	return c.ServerPort
}

func (c Server) GetHttpServerShutDownTimeout() time.Duration {
	return c.ServerShutDownTimeout
}

func (c Server) GetHttpServerRequestTimeout() time.Duration {
	return c.ServerRequestTimeout
}
