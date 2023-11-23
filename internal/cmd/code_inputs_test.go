package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/runlog"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeRunInputs is a daemon with one run's requests.
type fakeRunInputs struct {
	mu   sync.Mutex
	list *pb.ListRunInputsResponse
	// lists, when set, are returned in turn by successive ListRunInputs calls
	// (the last one repeats).
	lists    []*pb.ListRunInputsResponse
	answer   *pb.AnswerRunInputResponse
	answers  []*pb.AnswerRunInputRequest
	onAnswer func() // runs as the answer is sent
}

func (f *fakeRunInputs) ListRunInputs(*pb.ListRunInputsRequest) (*pb.ListRunInputsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.lists) > 0 {
		l := f.lists[0]
		if len(f.lists) > 1 {
			f.lists = f.lists[1:]
		}
		return l, nil
	}
	return f.list, nil
}

func (f *fakeRunInputs) AnswerRunInput(req *pb.AnswerRunInputRequest) (*pb.AnswerRunInputResponse, error) {
	if f.onAnswer != nil {
		f.onAnswer()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, req)
	return f.answer, nil
}

func (f *fakeRunInputs) GetRunApprovalKey(string) (*pb.GetRunApprovalKeyResponse, error) {
	return nil, errors.New("not used")
}

func (f *fakeRunInputs) Close() error { return nil }

func (f *fakeRunInputs) sent() []*pb.AnswerRunInputRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*pb.AnswerRunInputRequest(nil), f.answers...)
}

// inputFor is the complete Bash input behind a "Bash: <command>" prompt.
func inputFor(prompt string) string {
	b, _ := json.Marshal(map[string]string{"command": strings.TrimPrefix(prompt, "Bash: ")})
	return string(b)
}

// digestOf is the request digest an answer for that prompt's request binds.
func digestOf(prompt string) []byte { return coderun.RequestDigest("Bash", inputFor(prompt)) }

func openRequest(now time.Time, id, prompt string) *pb.RunInput {
	return &pb.RunInput{RequestId: id, Kind: pb.RunInputKind_RUN_INPUT_KIND_PERMISSION, Tool: "Bash", Prompt: prompt, Input: inputFor(prompt),
		CreatedAt: timestamppb.New(now.Add(-90 * time.Second)), ExpiresAt: timestamppb.New(now.Add(10 * time.Minute))}
}

func TestCodePendingListsOpenRequests(t *testing.T) {
	now := time.Now()
	api := &fakeRunInputs{list: &pb.ListRunInputsResponse{Running: true, Requests: 3, Allowed: 1, Expired: 1,
		Open: []*pb.RunInput{openRequest(now, "req-0123456789abcdef", "Bash: git push\x1b[2K")}}}
	var out bytes.Buffer
	if err := codePending(api, "alice", "code", &out, now); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"REQUEST", "req-0123456789abcdef", "1m30s", "10m0s", `Bash: git push\x1b[2K`,
		"permission requests: 3 (allowed 1, denied 0, expired 1, cancelled 0, open 1)"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("a raw escape from the prompt reached the terminal:\n%q", got)
	}
}

func deployRequest(now time.Time) *pb.ListRunInputsResponse {
	return &pb.ListRunInputsResponse{Running: true, Open: []*pb.RunInput{openRequest(now, "req-0123456789abcdef", "Bash: make deploy")}}
}

var answeredAllow = &pb.AnswerRunInputResponse{Outcome: pb.RunInputAnswerOutcome_RUN_INPUT_ANSWER_OUTCOME_ANSWERED,
	ClosedAnswer: pb.RunInputAnswer_RUN_INPUT_ANSWER_ALLOW, CloseReason: pb.RunInputCloseReason_RUN_INPUT_CLOSE_REASON_ANSWERED}

// `code answer` prints the request, complete input included, and has an
// allow confirmed BEFORE anything is sent, and the answer is signed for the
// printed request (#2184 review: it used to be printed only after sending,
// and only as a one-line prompt).
func TestCodeAnswerConfirmsThePrintedPromptBeforeSending(t *testing.T) {
	api := &fakeRunInputs{list: deployRequest(time.Now()), answer: answeredAllow}
	var out bytes.Buffer
	var shownAtSend string
	api.onAnswer = func() { shownAtSend = out.String() }

	err := codeAnswer(api, "alice", "code", "req-0123456789abcdef", pb.RunInputAnswer_RUN_INPUT_ANSWER_ALLOW,
		answerConfirm{tty: true, in: strings.NewReader("y\n")}, &out)
	if err != nil {
		t.Fatalf("codeAnswer: %v", err)
	}
	want := "request req-0123456789abcdef: Bash: make deploy\n  an allow permits exactly this:\n  tool:  Bash\n  input: {\"command\":\"make deploy\"}\nallow? [y/N] "
	if !strings.Contains(shownAtSend, want) {
		t.Errorf("at send time the output was %q, want the prompt, the complete input and the question", shownAtSend)
	}
	sent := api.sent()
	if len(sent) != 1 || !bytes.Equal(sent[0].GetRequestSha256(), digestOf("Bash: make deploy")) ||
		sent[0].GetAnswer() != pb.RunInputAnswer_RUN_INPUT_ANSWER_ALLOW || sent[0].GetRunName() != "code" {
		t.Fatalf("sent %v, want an allow bound to the printed prompt", sent)
	}
	if !strings.Contains(out.String(), "allowed req-0123456789abcdef: Bash: make deploy") {
		t.Errorf("output = %q", out.String())
	}
}

// The raw tool is shown before an allow, not only the prompt: the prompt is
// the agent's to write, and the answer is signed for the tool (#2184 review).
func TestCodeAnswerShowsTheRawToolBeforeAnAllow(t *testing.T) {
	req := &pb.RunInput{RequestId: "req-0123456789abcdef", Tool: "mcp__deploy__delete_environment",
		Prompt: "Inventory lookup: prod", Input: `{"name":"prod"}`}
	api := &fakeRunInputs{list: &pb.ListRunInputsResponse{Running: true, Open: []*pb.RunInput{req}}, answer: answeredAllow}
	var out bytes.Buffer
	var shownAtSend string
	api.onAnswer = func() { shownAtSend = out.String() }
	if err := codeAnswer(api, "alice", "code", req.RequestId, pb.RunInputAnswer_RUN_INPUT_ANSWER_ALLOW,
		answerConfirm{tty: true, in: strings.NewReader("y\n")}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shownAtSend, "tool:  mcp__deploy__delete_environment") {
		t.Errorf("the tool was not shown before the allow was sent:\n%s", shownAtSend)
	}
	if sent := api.sent(); len(sent) != 1 || !bytes.Equal(sent[0].GetRequestSha256(), coderun.RequestDigest(req.Tool, req.Input)) {
		t.Fatalf("sent %v", sent)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// An allow is never sent for a request that could not be shown (#2184
// review): not by `code answer`, not inline.
func TestAnAllowIsNotSentWhenTheRequestCouldNotBeShown(t *testing.T) {
	api := &fakeRunInputs{list: deployRequest(time.Now()), answer: answeredAllow}
	err := codeAnswer(api, "alice", "code", "req-0123456789abcdef", pb.RunInputAnswer_RUN_INPUT_ANSWER_ALLOW,
		answerConfirm{tty: true, in: strings.NewReader("y\n")}, failingWriter{})
	if err == nil || len(api.sent()) != 0 {
		t.Errorf("code answer: err = %v, sent %d", err, len(api.sent()))
	}

	w := &inlineAnswers{api: api, box: "alice", run: "code", diag: failingWriter{}, shown: map[string]*pb.RunInput{}}
	w.refresh()
	w.read(context.Background(), strings.NewReader("a\n"))
	if len(api.sent()) != 0 {
		t.Error("inline: an allow was sent for a request that was never printed")
	}
}

func TestCodeAnswerAllowNeedsConfirmation(t *testing.T) {
	cases := map[string]answerConfirm{
		"answered no":           {tty: true, in: strings.NewReader("n\n")},
		"no answer at all":      {tty: true, in: strings.NewReader("")},
		"no terminal, no --yes": {in: strings.NewReader("y\n")},
	}
	for name, c := range cases {
		api := &fakeRunInputs{list: deployRequest(time.Now()), answer: answeredAllow}
		err := codeAnswer(api, "alice", "code", "req-0123456789abcdef", pb.RunInputAnswer_RUN_INPUT_ANSWER_ALLOW, c, &bytes.Buffer{})
		if err == nil {
			t.Errorf("%s: allowed without confirmation", name)
		}
		if len(api.sent()) != 0 {
			t.Errorf("%s: an answer was sent", name)
		}
	}
}

// --yes sends an allow without a terminal; a deny is never asked about.
func TestCodeAnswerYesAndDenyNeedNoQuestion(t *testing.T) {
	for name, c := range map[string]struct {
		a   pb.RunInputAnswer
		yes bool
	}{
		"allow --yes": {pb.RunInputAnswer_RUN_INPUT_ANSWER_ALLOW, true},
