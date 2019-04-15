package containariumotel

import (
	"regexp"
	"testing"
)

// semverish accepts the relaxed versions Containarium tags use
// (0.20.0, 0.20.0-rc.1, 0.20.0+sha.abcd). Whatever the VERSION file
// says, the parsed `version` should look like a version.
var semverish = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-.+][\w.-]+)?$`)

func TestVersion_NonEmpty(t *testing.T) {
	if version == "" {
		t.Fatal("version is empty — VERSION file may not have been embedded")
	}
}

func TestVersion_LooksLikeVersion(t *testing.T) {
	if !semverish.MatchString(version) {
		t.Errorf("version %q doesn't look like a version string", version)
	}
}

