package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestCheckDowngrade(t *testing.T) {
	cases := []struct {
		name         string
		running, tag string
		allow        bool
		wantRefuse   bool
	}{
		{"older tag refused", "0.118.0", "v0.117.0", false, true},
		{"older tag, allowed", "0.118.0", "v0.117.0", true, false},
		{"equal tag", "0.118.0", "v0.118.0", false, false},
		{"newer tag", "0.118.0", "v0.119.0", false, false},
		{"tag without v prefix, older", "0.118.0", "0.100.0", false, true},
		{"numeric not lexical: 0.9.0 < 0.118.0", "0.118.0", "v0.9.0", false, true},
		{"numeric not lexical: 0.118.0 > 0.99.0 is newer", "0.99.0", "v0.118.0", false, false},
		{"pre-release of running release is older", "0.118.0", "v0.118.0-rc1", false, true},
		{"release is newer than running pre-release", "0.118.0-rc1", "v0.118.0", false, false},
		{"latest is never refused", "0.118.0", "latest", false, false},
