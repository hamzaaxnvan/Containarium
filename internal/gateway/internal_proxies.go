package gateway

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// mountInternalProxies wires the Grafana, Alertmanager, and
// Guacamole reverse proxies onto `mux`, gated by the gateway's
// JWT middleware.
//
