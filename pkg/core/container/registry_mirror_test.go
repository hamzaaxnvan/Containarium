package container

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

func TestParseRegistryMirrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []RegistryMirror
	}{
		{"empty", "", nil},
		{"whitespace", "   ", nil},
		{
			"single http is insecure",
			"docker.io=http://mirror.lan:5000",
			[]RegistryMirror{{Upstream: "docker.io", Location: "mirror.lan:5000", Insecure: true}},
		},
		{
			"https is secure",
			"ghcr.io=https://mirror.lan:5000",
			[]RegistryMirror{{Upstream: "ghcr.io", Location: "mirror.lan:5000", Insecure: false}},
