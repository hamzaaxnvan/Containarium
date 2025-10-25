package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGcloudUploader_DownloadStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake gcloud executable")
	}
	for _, tc := range []struct {
		name, script string
