//go:build !windows && !containarium_client

package cmd

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/sentinel"
)

const (
	testPinA = "sha256:" + "aa00000000000000000000000000000000000000000000000000000000000000"
	testPinB = "sha256:" + "bb00000000000000000000000000000000000000000000000000000000000000"
)

func TestResolveSentinelPins(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		env     string
