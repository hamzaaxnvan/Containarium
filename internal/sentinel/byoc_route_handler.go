package sentinel

import (
	"encoding/json"
	"log"
	"net/http"
)

// BYOCRouteSyncRequest is the JSON body POSTed to BYOCRouteRegisterHandler
// by the cloud to push the FULL authoritative set of BYOC public-ingress
// bindings. Full-set (not delta): the sentinel replaces its map wholesale,
// so a binding the cloud dropped disappears without a separate delete, and
// the state converges after a sentinel restart or a missed event. #733.
type BYOCRouteSyncRequest struct {
	Routes []BYOCRoute `json:"routes"`
}

