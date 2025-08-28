package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScanOptIn_RequiresServer(t *testing.T) {
	old := serverAddr
	serverAddr = ""
	defer func() { serverAddr = old }()
	if err := runScanOptInSet(testCmd(), "alice-container", true); err == nil {
