package gateway

import "testing"

// A client must not be able to smuggle identity into gRPC metadata via
// Grpc-Metadata-* request headers; identity is written only by annotateContext.
func TestIncomingHeaderMatcher_DropsReservedIdentityKeys(t *testing.T) {
	for _, h := range []string{
		"Grpc-Metadata-Roles", "Grpc-Metadata-Username", "grpc-metadata-scopes",
		"Grpc-Metadata-Act", "Grpc-Metadata-Jti", "Grpc-Metadata-Run_id",
