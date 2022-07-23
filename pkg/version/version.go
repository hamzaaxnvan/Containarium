package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	// Version is the semantic version. Release builds override this via
	// ldflags (`make build-release VERSION=<tag>` in .github/workflows/release.yml),
	// so the tag is the source of truth; keep this constant in sync as the
	// fallback for plain `make build` / `go build`.
	Version = "0.122.0"

	// GitCommit is the git commit hash (set by build flag via ldflags)
	GitCommit = ""

	// BuildTime is the build timestamp (set by build flag via ldflags)
	BuildTime = ""

	// GoVersion is the Go version used to build
	GoVersion = runtime.Version()

	// Platform is the OS/Arch
	Platform = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
)

// Info contains all version information
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// GetVersion returns the current version
func GetVersion() string {
	return Version
}

