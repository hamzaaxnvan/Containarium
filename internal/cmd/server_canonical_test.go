package cmd

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/credentials"
)

func seedServerCreds(t *testing.T, servers ...string) {
	t.Helper()
	path, err := credentials.DefaultPath()
	if err != nil {
		t.Fatal(err)
