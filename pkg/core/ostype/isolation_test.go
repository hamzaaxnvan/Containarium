package ostype

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestParseIsolation(t *testing.T) {
	tests := []struct {
		in      string
		want    pb.IsolationType
		wantErr bool
