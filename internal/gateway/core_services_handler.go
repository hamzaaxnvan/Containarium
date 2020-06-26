package gateway

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// CoreServicesHandler handles core infrastructure service queries.
type CoreServicesHandler struct {
	client *incus.Client
}

// NewCoreServicesHandler creates a new handler (connects to Incus directly).
func NewCoreServicesHandler() (*CoreServicesHandler, error) {
	client, err := incus.New()
	if err != nil {
		return nil, err
	}
	return &CoreServicesHandler{client: client}, nil
}
