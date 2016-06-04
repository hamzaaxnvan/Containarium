package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// CodingToolEgressPolicyService REST calls (#2378): method + path must match
// the google.api.http mapping in code_egress.proto:
//
//	PUT    /v1/code/egress-policy/{policy.tenant}   (and /v1/code/egress-policy)
