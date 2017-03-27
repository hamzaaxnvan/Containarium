package cmd

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestParseToolAccess(t *testing.T) {
	for in, want := range map[string]pb.BoxToolAccess{
		"none":        pb.BoxToolAccess_BOX_TOOL_ACCESS_NONE,
		"build-tools": pb.BoxToolAccess_BOX_TOOL_ACCESS_BUILD_TOOLS,
		"BUILD_TOOLS": pb.BoxToolAccess_BOX_TOOL_ACCESS_BUILD_TOOLS,
		" full-sudo ": pb.BoxToolAccess_BOX_TOOL_ACCESS_FULL_SUDO,
	} {
		got, err := parseToolAccess(in)
		if err != nil || got != want {
			t.Errorf("parseToolAccess(%q) = %v, %v; want %v", in, got, err, want)
		}
