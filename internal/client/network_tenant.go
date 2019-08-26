package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// networkTenantSourceFromProto maps the wire enum to the provider-source
// string. The UNSPECIFIED zero value becomes "" so an old daemon (which never
// sets the field) and the HTTP client (which omits absent fields) agree, and
// `containarium get` does not print a tenant line for every un-tenanted box.
func networkTenantSourceFromProto(s pb.NetworkTenantSource) incus.NetworkTenantSource {
	if s == pb.NetworkTenantSource_NETWORK_TENANT_SOURCE_UNSPECIFIED {
		return ""
	}
	return incus.NetworkTenantSource(s.String())
