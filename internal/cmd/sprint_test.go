package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSprint runs `containarium sprint <args>` against an httptest server and
// returns stdout and the command error (non-nil means a non-zero exit).
func runSprint(t *testing.T, status int, body string, seen *http.Request, args ...string) (string, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = *r
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	oldServer, oldToken := serverAddr, authToken
	t.Cleanup(func() { serverAddr, authToken = oldServer, oldToken })
	t.Setenv("HOME", t.TempDir()) // never read a developer's credentials file
	return execSprint(t, append([]string{"--server", srv.URL, "--token", "tok"}, args...))
}

func execSprint(t *testing.T, args []string) (string, error) {
	t.Helper()
	// cobra keeps flag values and Changed marks across Execute calls.
	for _, c := range []*cobra.Command{sprintBoardCmd, sprintListCmd} {
		c.Flags().VisitAll(func(f *pflag.Flag) { _ = f.Value.Set(f.DefValue); f.Changed = false })
	}
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs(append([]string{"sprint"}, args...))
	t.Cleanup(func() { rootCmd.SetArgs(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil) })
	err := rootCmd.Execute()
	return out.String(), err
}

const sprintBoardBody = `{"mirror":{"state":"SPRINT_MIRROR_STATE_PRESENT","renderedMarkdown":"## Sprint 3\n- #4 ready",
 "board":{"project":"p","umbrella":"3","umbrellaTitle":"Sprint 3","items":[{"number":"4","title":"T","state":"SPRINT_ITEM_STATE_READY"}]}}}`

func TestSprintBoard_PrintsMarkdownAndSummary(t *testing.T) {
	var seen http.Request
	out, err := runSprint(t, 200, sprintBoardBody, &seen, "board", "--org", "o", "--project", "p", "--number", "3")
	require.NoError(t, err)
	assert.Equal(t, "/v1/orgs/o/sprints/board", seen.URL.Path)
	assert.Equal(t, "p", seen.URL.Query().Get("umbrella.project"))
	assert.Equal(t, "3", seen.URL.Query().Get("umbrella.number"))
	assert.Equal(t, "TRACKER_PROVIDER_GITHUB", seen.URL.Query().Get("umbrella.provider"))
	assert.Contains(t, out, "1 items")
	assert.Contains(t, out, "## Sprint 3\n- #4 ready")
}

func TestSprintBoard_GitLabProvider(t *testing.T) {
	var seen http.Request
