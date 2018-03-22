package toolaccess

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestRoundTrip(t *testing.T) {
	for _, a := range []pb.BoxToolAccess{
		pb.BoxToolAccess_BOX_TOOL_ACCESS_NONE,
		pb.BoxToolAccess_BOX_TOOL_ACCESS_BUILD_TOOLS,
		pb.BoxToolAccess_BOX_TOOL_ACCESS_FULL_SUDO,
	} {
		v, ok := ConfigValue(a)
		if !ok || v == "" {
			t.Fatalf("%v has no config value", a)
		}
		if got := FromConfig(v); got != a {
			t.Errorf("%v -> %q -> %v", a, v, got)
		}
	}
