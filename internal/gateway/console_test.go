package gateway

import (
	"testing"
)

func TestParseConsoleUsername(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/v1/containers/alice/console-attach", "alice"},
		{"v1/containers/alice/console-attach", "alice"},
		{"/v1/containers/alice/console-attach/", "alice"},
		{"/v1/containers/alice/terminal", ""},
		{"/v1/containers/alice/console-attach/extra", ""},
		{"/v1/containers//console-attach", ""},
