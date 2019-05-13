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

	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = seedCreds(t, home, "", map[string]credentials.ServerCreds{
		"https://cloud.example":  {Token: "eyJcloud", AccessModel: credentials.AccessModelToken},
		"https://daemon.example": {Token: "eyJdaemon", AccessModel: credentials.AccessModelSSHKey},
	})

	// A cloud login whose token isn't prefix-identifiable → cloud via AccessModel.
	if !isCloudTarget("https://cloud.example", "eyJcloud") {
		t.Error("server with AccessModelToken must classify as cloud")
	}
	// Self-hosted AccessModel + a JWT → not cloud.
	if isCloudTarget("https://daemon.example", "eyJdaemon") {
