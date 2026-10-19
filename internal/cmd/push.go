package cmd

import (
	"fmt"
	"os"

	"github.com/footprintai/containarium/internal/transfer"
	"github.com/spf13/cobra"
)

var (
	pushUser         string
	pushBranch       string
	pushRemotePath   string
	pushSentinelHost string
	pushKeyPath      string
	pushIncludeWIP   bool
	pushDeployCmd    string
	pushRemoteName   string
	pushVerbose      bool
)

var pushCmd = &cobra.Command{
	Use:   "push <username> [local-path]",
	Short: "Push committed git history into a container",
	Long: `Push committed git history into a container via real ` + "`git push`" + ` over
the SSH path (laptop -> sentinel -> sshpiper -> container).

On first call, sets up a bare git repo at ~/work.git inside the
container plus a post-receive hook that checks out the working tree
to ~/work and optionally runs --deploy-cmd. On subsequent calls,
just runs ` + "`git push`" + `; the hook fires server-side.

The local working tree must be clean unless --include-wip is set, in
which case uncommitted + untracked changes are wrapped in a WIP
commit and the local repo is rewound after the push.

After the first call, a local git remote (default
"containarium-<username>") is configured. You can also bypass this
CLI entirely and just run:
  git push containarium-<username> <branch>
from the same local clone — same plumbing.

For mirror-semantics (uncommitted changes carried over as working-tree
state, not as a commit), use ` + "`containarium sync`" + ` instead.

Examples:
  # Push current branch from cwd to the container's default ~/work
  containarium push demo-blog

  # Push and run a deploy command after each successful update
  containarium push demo-blog --deploy-cmd "systemctl --user restart blog"

  # Push a specific branch with WIP autocommit-and-rewind
  containarium push demo-blog --branch feature/foo --include-wip

  # Push from a different local repo to a custom remote path
  containarium push demo-blog /path/to/local --remote-path /srv/app`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runPush,
}

func init() {
	rootCmd.AddCommand(pushCmd)
	pushCmd.Flags().StringVar(&pushBranch, "branch", "", "Git branch to push (default: current HEAD branch)")
	pushCmd.Flags().StringVar(&pushRemotePath, "remote-path", "", "Working-tree directory inside the container (default: ~/work). Bare repo lives at <remote-path>.git.")
	pushCmd.Flags().StringVar(&pushSentinelHost, "sentinel", "", "Sentinel SSH host (default: $CONTAINARIUM_SENTINEL_HOST)")
	pushCmd.Flags().StringVar(&pushKeyPath, "key", "", "SSH key path (default: ~/.containarium/keys/<username>)")
	pushCmd.Flags().BoolVar(&pushIncludeWIP, "include-wip", false, "Auto-commit uncommitted changes as a WIP commit and rewind after push")
	pushCmd.Flags().StringVar(&pushDeployCmd, "deploy-cmd", "", "Shell command to run on the container after each successful push (inside the work-tree directory)")
	pushCmd.Flags().StringVar(&pushRemoteName, "remote-name", "", "Local git remote name to configure (default: containarium-<username>)")
	pushCmd.Flags().BoolVarP(&pushVerbose, "verbose", "v", false, "Verbose progress on stderr")
}

func runPush(cmd *cobra.Command, args []string) error {
	pushUser = args[0]
	localPath := ""
	if len(args) > 1 {
		localPath = args[1]
	}

	res, err := transfer.Push(transfer.PushOptions{
		Options: transfer.Options{
			Username:     pushUser,
			SentinelHost: pushSentinelHost,
			KeyPath:      pushKeyPath,
			LocalPath:    localPath,
			RemotePath:   pushRemotePath,
			Verbose:      pushVerbose,
		},
		Branch:     pushBranch,
		IncludeWIP: pushIncludeWIP,
		DeployCmd:  pushDeployCmd,
		RemoteName: pushRemoteName,
	})
	if err != nil {
		return err
	}

	if res.PreviousHead == "" {
		fmt.Fprintf(os.Stdout, "pushed branch %s to %s (first push, head=%s)\n",
			res.Branch, res.RemoteURL, shortSha(res.NewHead))
	} else {
		fmt.Fprintf(os.Stdout, "pushed branch %s: %s..%s -> %s\n",
			res.Branch, shortSha(res.PreviousHead), shortSha(res.NewHead), res.RemoteURL)
	}
	if res.DeployCmd != "" {
		fmt.Fprintf(os.Stdout, "  deploy hook configured: %s\n", res.DeployCmd)
	}
	if res.WIPCommitMade {
		fmt.Fprintln(os.Stdout, "  note: WIP commit shipped and local repo rewound to pre-WIP state")
	}
	return nil
}

func shortSha(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
