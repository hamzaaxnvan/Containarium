package gateway

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// CertPair holds a domain's certificate and key in PEM format.
type CertPair struct {
	Domain  string `json:"domain"`
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

// CertsResponse is the JSON response from the /certs endpoint.
type CertsResponse struct {
	Certs []CertPair `json:"certs"`
}

// ServeCerts returns an HTTP handler that walks Caddy's certificate directory
// and returns all cert/key pairs as JSON. This allows the sentinel to sync
// real Let's Encrypt certificates for use during maintenance mode.
//
// Caddy stores LE certs under:
//
//	<certBaseDir>/certificates/<issuer>/<domain>/<domain>.crt
//	<certBaseDir>/certificates/<issuer>/<domain>/<domain>.key
func ServeCerts(certBaseDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if certBaseDir == "" {
			http.Error(w, `{"error":"cert base dir not configured"}`, http.StatusServiceUnavailable)
			return
		}

