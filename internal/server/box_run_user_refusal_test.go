package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/coderun/engine"
	"github.com/footprintai/containarium/internal/gateway"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// violationTypeOf returns the type of the single PreconditionFailure violation on
// err, or "" if it carries none.
func violationTypeOf(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return ""
	}
	for _, d := range st.Details() {
		if p, ok := d.(*errdetails.PreconditionFailure); ok && len(p.GetViolations()) == 1 {
			return p.GetViolations()[0].GetType()
		}
	}
	return ""
}

// A refusal for want of a run-user sign-in carries a TYPED reason, so the hosted
