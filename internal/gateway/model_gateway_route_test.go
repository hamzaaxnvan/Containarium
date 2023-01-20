package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/modelgateway"
)

// TestModelGatewayDiagnosticsRouting locks the embedded-daemon routing fix:
// the model-gateway handler is mounted under /v1/model/, but its #794
// /__gateway/status diagnostics gauge must ALSO be reachable at the daemon
// root — otherwise it falls through to the wake catch-all (the bug this fix
