package backup

import (
	"errors"
	"testing"
)

// A missing object on `gcloud storage rm` maps to ErrObjectNotFound so
// Delete can tell "nothing to delete" (the sidecar of a record written
// before #2402 uploaded sidecars) apart from a real failure, which keeps
// its gcloud output and is reported.
func TestGcloudUploader_DeleteMapsNotFound(t *testing.T) {
	for _, tc := range []struct {
		name     string
		out      string
		fail     bool
		notFound bool
	}{
		{"gcloud storage phrasing", "ERROR: (gcloud.storage.rm) The following URLs matched no objects or files:\ngs://b/x.meta.json", true, true},
		{"gsutil phrasing", "CommandException: No URLs matched: gs://b/x.meta.json", true, true},
		{"api not found", "ERROR: HTTPError 404: NotFound", true, true},
