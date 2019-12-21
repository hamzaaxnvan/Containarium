package sentinel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"time"
)

//go:embed maintenance.html
var maintenancePage []byte

// maintenanceHandler returns the default maintenance page handler.
func maintenanceHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write(maintenancePage)
	}
}

// NewMaintenanceServer creates an HTTP server that serves the maintenance page
// on all paths with a 503 status code. The /sentinel path serves the status page.
