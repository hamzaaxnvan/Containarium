//go:build !linux

package hostcheck

import "errors"

// The egress program is Linux-only; elsewhere every probe reports
// "could not determine", so the checks fail rather than pass.
func defaultEgressPrereqProbes() egressPrereqProbes {
