package ospkg

import (
	"github.com/footprintai/containarium/pkg/core/ostype"
)

// PackageManager abstracts OS-specific package management and user operations.
type PackageManager interface {
	// UpdateCmd returns the command to update package repo metadata.
	UpdateCmd() []string

