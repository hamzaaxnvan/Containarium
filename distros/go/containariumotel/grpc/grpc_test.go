package grpc

import "testing"

func TestNewServerHandler_NonNil(t *testing.T) {
	if h := NewServerHandler(); h == nil {
		t.Fatal("NewServerHandler returned nil")
	}
}

