package platformstats

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUnaryInterceptor_ClassifiesByFinalStatus(t *testing.T) {
	tests := []struct {
		name       string
		handlerErr error
		wantClass  CodeClass
	}{
		{"nil error is ok", nil, CodeClassOK},
		{"InvalidArgument status is client_error", status.Error(codes.InvalidArgument, "bad"), CodeClassClientError},
		{"NotFound status is client_error", status.Error(codes.NotFound, "missing"), CodeClassClientError},
		{"Internal status is server_error", status.Error(codes.Internal, "boom"), CodeClassServerError},
		{"plain non-status error maps to Unknown -> server_error", errors.New("plain error"), CodeClassServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stats := New()
			interceptor := UnaryInterceptor(stats)

			handler := func(ctx context.Context, req interface{}) (interface{}, error) {
				return "resp", tc.handlerErr
			}
			info := &grpc.UnaryServerInfo{FullMethod: "/containarium.v1.ContainerService/CreateContainer"}

			resp, err := interceptor(context.Background(), "req", info, handler)
			if !errors.Is(err, tc.handlerErr) && tc.handlerErr != nil && err.Error() != tc.handlerErr.Error() {
				t.Errorf("interceptor changed the error: got %v, want %v", err, tc.handlerErr)
			}
			if tc.handlerErr == nil && resp != "resp" {
