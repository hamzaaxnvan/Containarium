package container

import "testing"

// TestBakedImageAliasFor pins the alias derivation: deterministic, and free
// of ":" and "/" so the incus client resolves it as a LOCAL alias (a ":"
// would read as a remote prefix, a "/" as a simplestreams path — either
// would silently turn the fast path into a network pull).
func TestBakedImageAliasFor(t *testing.T) {
	cases := []struct {
		image string
		want  string
	}{
		{"images:ubuntu/24.04", "containarium-base-images-ubuntu-24-04"},
		{"ubuntu:24.04", "containarium-base-ubuntu-24-04"},
		{"ubuntu/24.04", "containarium-base-ubuntu-24-04"},
		{"UBUNTU/24.04", "containarium-base-ubuntu-24-04"},
	}
	for _, c := range cases {
		got := BakedImageAliasFor(c.image)
		if got != c.want {
			t.Errorf("BakedImageAliasFor(%q) = %q, want %q", c.image, got, c.want)
		}
		for _, forbidden := range []byte{':', '/'} {
			for i := 0; i < len(got); i++ {
				if got[i] == forbidden {
					t.Errorf("alias %q contains %q — would not resolve as a local alias", got, string(forbidden))
				}
			}
		}
	}
