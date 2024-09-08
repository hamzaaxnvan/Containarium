package container

import (
	"strings"
	"testing"
)

func TestRenderEnvFile(t *testing.T) {
	header := "# generated, do not edit\n"
	env := map[string]string{
		"ZED":         "last",
		"ALPHA":       "first",
		"WITH_EQUALS": "Authorization=Bearer abc123", // value keeps its '='
