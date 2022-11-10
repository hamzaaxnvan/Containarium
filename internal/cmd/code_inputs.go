package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/runlog"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Permission requests of a `code run --approvals ask` run (#2184), answered
// through the daemon API: the daemon holds the box's approval key and signs.

var codePendingCmd = &cobra.Command{
	Use:   "pending <box>",
	Short: "List a run's open permission requests",
	Long: `Lists the open permission requests of a run started with
'code run --approvals ask': request id, age, time left before it expires into
a deny, and the prompt (the tool and its command or path), followed by the
run's request counts. Answer one with 'containarium code answer'.

The list comes from the run's journal on the box, which the agent can write
too, so treat it as advisory: an answer is signed for the prompt it was given,
and the run ignores it unless that is the prompt it actually asked.

Reads through the daemon API (--server).`,
	Args: cobra.ExactArgs(1),
	RunE: runCodePending,
}

var codeAnswerCmd = &cobra.Command{
	Use:   "answer <box> <request_id> allow|deny",
	Short: "Answer one permission request of a run",
	Long: `Answers one open permission request of a run started with
'code run --approvals ask'.

The request is read again and printed before anything is sent: its one-line
prompt and the tool's complete input, which is what an allow permits. The
answer is signed for exactly that tool and input, so the run acts on it only
if that is the tool call it asked. An allow needs confirmation: at a terminal
you are asked "allow? [y/N]" under the printed request. Without a terminal,
pass --yes; the allow is then signed for whatever the request is at that
moment, which you have not seen, so prefer answering at a terminal or inline
in 'code attach'. A deny is sent without asking. If the request changes
between printing and sending, nothing is sent.

Answering a request that is already answered or has expired changes nothing,
prints how it was closed, and exits non-zero; so does an answer the run has
not reported taking within a few seconds. That report comes from the run's
journal, which the agent can write, so it is not proof the tool call ran.

Answers through the daemon API (--server). The token needs the code:answer
scope as well as ssh:write; the wildcard scope and a token with no scopes do
not count. No credential the platform puts on a box carries code:answer, so
keep the token that does off the box: an operator mints it on the daemon host
with 'containarium token generate --secret-file <file> --username <you>
--scopes ssh:write,code:answer' and you pass it with --token or
CONTAINARIUM_TOKEN.
Do not run 'containarium login' inside the box the agent runs in: the
credentials it writes there are readable by the agent.`,
	Args: cobra.ExactArgs(3),
	RunE: runCodeAnswer,
}

var codeAnswerYes bool

func init() {
	codeCmd.AddCommand(codePendingCmd)
	codeCmd.AddCommand(codeAnswerCmd)
	for _, c := range []*cobra.Command{codePendingCmd, codeAnswerCmd} {
		c.Flags().StringVar(&codeName, "name", "", "run name (default: \""+defaultCodeRunName+"\")")
	}
	codeAnswerCmd.Flags().BoolVar(&codeAnswerYes, "yes", false,
		"send an allow without asking (needed without a terminal); the allow is signed for the request current at that moment, unseen")
}

// runInputsAPI is what pending/answer/attach and `code run --approvals ask`
// need from a daemon client.
type runInputsAPI interface {
	runlog.RunInputs
	GetRunApprovalKey(username string) (*pb.GetRunApprovalKeyResponse, error)
	Close() error
}

// newRunInputsClient is the daemon client for the permission-request RPCs:
// the same --server/--http choice as `code runs|logs`.
var newRunInputsClient = func() (runInputsAPI, error) {
	if serverAddr == "" {
		return nil, fmt.Errorf("--server is required to read or answer permission requests (they are answered through the daemon)")
	}
	if httpMode {
		return client.NewHTTPClient(serverAddr, authToken)
	}
	return client.NewGRPCClient(serverAddr, certsDir, insecure)
}

func runCodePending(cmd *cobra.Command, args []string) error {
	box := args[0]
	if err := validateBoxName(box); err != nil {
		return err
	}
	c, err := newRunInputsClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return codePending(c, box, codeRunName(), cmd.OutOrStdout(), time.Now())
}

// codePending prints run's open requests through the formatter the MCP
// code_pending tool also uses.
func codePending(api runlog.RunInputs, box, run string, w io.Writer, now time.Time) error {
	resp, err := api.ListRunInputs(&pb.ListRunInputsRequest{Username: box, RunName: run})
	if err != nil {
		return fmt.Errorf("list permission requests of run %s on %s: %w", run, box, err)
	}
	return runlog.WriteRunInputs(w, run, resp, now)
}

func runCodeAnswer(cmd *cobra.Command, args []string) error {
	box, id := args[0], args[1]
	if err := validateBoxName(box); err != nil {
		return err
	}
	a, err := runlog.ParseRunInputAnswer(args[2])
	if err != nil {
		return err
	}
	c, err := newRunInputsClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return codeAnswer(c, box, codeRunName(), id, a, answerConfirm{yes: codeAnswerYes, tty: stdinIsTerminal(), in: cmd.InOrStdin()}, cmd.OutOrStdout())
}

// answerConfirm is how `code answer` gets a human's yes for an allow.
type answerConfirm struct {
	yes bool      // --yes: do not ask
	tty bool      // stdin is a terminal
	in  io.Reader // where the y/N answer is read
}

// codeAnswer prints the request (its prompt and complete input), has an
// allow confirmed, and sends the answer signed for the printed request through
// runlog.Answer, the function the MCP code_answer tool also calls. Anything
// but a fresh answer the run reported taking is an error, so the command
// exits non-zero.
func codeAnswer(api runlog.RunInputs, box, run, id string, a pb.RunInputAnswer, confirm answerConfirm, w io.Writer) error {
	shown, err := runlog.OpenRequest(api, box, run, id)
	if err != nil {
		return err
	}
	if shown != nil {
		if err := runlog.WriteRequest(w, shown); err != nil {
			return fmt.Errorf("could not show request %s, so nothing was sent: %w", id, err)
		}
		if a == pb.RunInputAnswer_RUN_INPUT_ANSWER_ALLOW && !confirm.yes {
			if !confirm.tty {
				return fmt.Errorf("an allow needs confirmation: run 'code answer' at a terminal, or pass --yes to allow whatever the request is when it is sent")
			}
			if _, err := fmt.Fprint(w, "allow? [y/N] "); err != nil {
				return fmt.Errorf("could not ask for confirmation, so nothing was sent: %w", err)
			}
			line, _ := bufio.NewReader(confirm.in).ReadString('\n')
			if s := strings.ToLower(strings.TrimSpace(line)); s != "y" && s != "yes" {
