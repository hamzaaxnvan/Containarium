//go:build !windows

package hostcheck

import (
	"errors"
	"strings"
	"testing"
)

func TestNICGuardCheck(t *testing.T) {
	tests := []struct {
