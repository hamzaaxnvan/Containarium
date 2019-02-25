package server

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/pkg/core/backup"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #3169: require_encryption refuses only the case that would be stored with
// no key at all. Every other combination, and every request without the
// flag, behaves exactly as before.
func TestRefusePlaintext(t *testing.T) {
	for _, tc := range []struct {
		name       string
		require    bool
		mode       backup.KeyMode
		recipient  string
		hasWrapper bool
		wantRefuse bool
	}{
		{"required, nothing to encrypt to: refused", true, "", "", false, true},
		{"required, registered or given recipient", true, "", "age1x", false, false},
