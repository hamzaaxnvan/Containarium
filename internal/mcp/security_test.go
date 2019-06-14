package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeZapRisk(t *testing.T) {
	cases := []struct{ in, want string }{
		{"High", "high"},
		{"medium", "medium"},
		{"LOW", "low"},
		{"Informational", "info"},
		{"Info", "info"},
