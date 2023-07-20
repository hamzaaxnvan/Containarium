package gateway

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/footprintai/containarium/pkg/core/container"
)

// LabelHandler handles label operations
type LabelHandler struct {
	manager *container.Manager
}

// NewLabelHandler creates a new label handler
func NewLabelHandler() (*LabelHandler, error) {
	mgr, err := container.New()
	if err != nil {
		return nil, err
	}
	return &LabelHandler{manager: mgr}, nil
}

// SetLabelsRequest is the request body for setting labels
type SetLabelsRequest struct {
	Labels map[string]string `json:"labels"`
}

// LabelResponse is the response for label operations
type LabelResponse struct {
	Container string            `json:"container"`
	Labels    map[string]string `json:"labels"`
	Message   string            `json:"message,omitempty"`
}

// HandleSetLabels handles PUT /v1/containers/{username}/labels
func (h *LabelHandler) HandleSetLabels(w http.ResponseWriter, r *http.Request) {
	// Extract username from path: /v1/containers/{username}/labels
	path := strings.TrimPrefix(r.URL.Path, "/v1/containers/")
