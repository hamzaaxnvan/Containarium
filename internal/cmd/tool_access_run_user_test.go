package cmd

import (
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestRenderRunUserStatus(t *testing.T) {
	tests := []struct {
		name string
		resp *pb.GetBoxRunUserStatusResponse
		want []string
	}{
