package transfer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderHook_NoDeployCmd(t *testing.T) {
	h, err := renderHook(hookData{
		Branch:   "main",
		BareRepo: "/home/alice/work.git",
		WorkTree: "/home/alice/work",
	})
	require.NoError(t, err)
	assert.Contains(t, h, `branch="${refname#refs/heads/}"`)
	assert.Contains(t, h, `if [ "$branch" = "main" ]; then`)
	assert.Contains(t, h, `GIT_WORK_TREE=/home/alice/work git --git-dir=/home/alice/work.git checkout -f "$branch"`)
	// No deploy block.
	assert.NotContains(t, h, "cd /home/alice/work")
}

func TestRenderHook_WithDeployCmd(t *testing.T) {
	h, err := renderHook(hookData{
		Branch:    "main",
		BareRepo:  "/home/alice/work.git",
		WorkTree:  "/home/alice/work",
		DeployCmd: "systemctl --user restart app",
	})
	require.NoError(t, err)
	assert.Contains(t, h, "cd /home/alice/work")
	assert.Contains(t, h, "systemctl --user restart app")
	// Order matters: cd should appear before the deploy_cmd.
	cdIdx := strings.Index(h, "cd /home/alice/work")
	cmdIdx := strings.Index(h, "systemctl")
	assert.Less(t, cdIdx, cmdIdx, "cd into work tree should precede deploy_cmd")
}

func TestRenderHook_MultilineDeployCmd(t *testing.T) {
	// Operator-supplied deploy commands can be multiple lines (e.g. a
	// build step then a restart). Make sure they're emitted verbatim.
	h, err := renderHook(hookData{
		Branch:    "main",
		BareRepo:  "/home/alice/work.git",
		WorkTree:  "/home/alice/work",
		DeployCmd: "make build\nsystemctl restart app",
	})
	require.NoError(t, err)
	assert.Contains(t, h, "make build")
	assert.Contains(t, h, "systemctl restart app")
}

func TestRemoteRepoSSHURL_DefaultHomePath(t *testing.T) {
	url := remoteRepoSSHURL(PushOptions{
		Options: Options{
			Username:     "alice",
			SentinelHost: "sentinel.example.com",
			RemotePath:   "/home/alice/work",
		},
	})
	// "/home/<user>/" prefix is stripped because git remote URLs of the
	// form "<user>@<host>:<rel>" are relative to the user's home dir on
	// the remote.
	assert.Equal(t, "alice@sentinel.example.com:work.git", url)
}

func TestRemoteRepoSSHURL_TildeAliasPath(t *testing.T) {
	url := remoteRepoSSHURL(PushOptions{
		Options: Options{
			Username:     "alice",
			SentinelHost: "sentinel.example.com",
			RemotePath:   "~/work",
		},
	})
	assert.Equal(t, "alice@sentinel.example.com:work.git", url)
}

func TestRemoteRepoSSHURL_AbsoluteOutsideHome(t *testing.T) {
	url := remoteRepoSSHURL(PushOptions{
		Options: Options{
			Username:     "alice",
			SentinelHost: "sentinel.example.com",
			RemotePath:   "/srv/app",
		},
	})
	// Absolute path outside user's home is passed through; git accepts it.
	assert.Equal(t, "alice@sentinel.example.com:/srv/app.git", url)
}

func TestGitSSHCommand_HasIdentitiesOnly(t *testing.T) {
	opt := PushOptions{Options: Options{KeyPath: "/tmp/key"}}
	cmd := gitSSHCommand(opt)
	// Must include IdentitiesOnly=yes — otherwise the SSH client offers
	// every ~/.ssh/* key, burning sshpiper's failtoban budget (the
	// load-bearing learning from PR #132).
	assert.Contains(t, cmd, "IdentitiesOnly=yes")
	assert.Contains(t, cmd, "-i /tmp/key")
	assert.True(t, strings.HasPrefix(cmd, "ssh "), "must start with ssh")
}
