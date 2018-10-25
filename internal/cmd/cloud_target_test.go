package cmd

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/credentials"
)

func TestIsCloudTarget(t *testing.T) {
	// A `ctnr_` token is a one-way cloud signal, independent of any creds file.
	if !isCloudTarget("https://anything", "ctnr_abc.def") {
		t.Fatal("ctnr_ token must classify as cloud")
	}
