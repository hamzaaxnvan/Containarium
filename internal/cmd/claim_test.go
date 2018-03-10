package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testClaimToken = "v1.anon-1a2b3c4d-container.ff00.1790870400.0123456789abcdef0123456789abcdef.aa"

func TestRenderClaim(t *testing.T) {
	tests := []struct {
		name    string
