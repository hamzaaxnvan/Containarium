package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestRunSecurityFindingsList_RequiresServer(t *testing.T) {
	oldServer := serverAddr
	serverAddr = ""
	defer func() { serverAddr = oldServer }()

	if err := runSecurityFindingsList(testCmd(), nil); err == nil {
		t.Fatal("expected an error when --server is unset")
	}
}

func TestRunSecurityFindingsList_HitsExpectedPathAndRendersOutput(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"findings": [
				{"id": "7", "rule": "THREAT_RULE_ID_BAD_DESTINATION", "severity": "THREAT_SEVERITY_HIGH",
				 "tenantId": "alice", "state": "FINDING_STATE_OPEN", "count": "3",
				 "lastSeen": "2026-08-31T10:00:00Z", "subject": "203.0.113.9"}
			]
		}`))
	}))
	defer srv.Close()

	oldServer := serverAddr
	serverAddr = srv.URL
	defer func() { serverAddr = oldServer }()

	oldSeverity, oldTenant, oldSince, oldState, oldLimit := findingsSeverity, findingsTenant, findingsSince, findingsState, findingsLimit
	findingsSeverity, findingsTenant = "", ""
	findingsSince, findingsState = "", ""
	findingsLimit = 0
	defer func() {
		findingsSeverity, findingsTenant, findingsSince, findingsState, findingsLimit = oldSeverity, oldTenant, oldSince, oldState, oldLimit
	}()

	cmd := testCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := runSecurityFindingsList(cmd, nil); err != nil {
		t.Fatalf("runSecurityFindingsList: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/security/findings" {
		t.Fatalf("request = %s %s, want GET /v1/security/findings", gotMethod, gotPath)
	}
	out := buf.String()
	for _, want := range []string{"BAD_DESTINATION", "HIGH", "OPEN", "alice", "203.0.113.9"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// #2775 review: the kill facts the webhook carries are visible from the CLI
// too, in the text line and in --json (the guide points operators here for
// a kill the rate-limited webhook has not delivered yet).
func TestRunSecurityFindingsList_RendersKillFacts(t *testing.T) {
	const canned = `{
		"findings": [
			{"id": "7", "rule": "THREAT_RULE_ID_MINER_SIGNATURE", "severity": "THREAT_SEVERITY_HIGH",
			 "tenantId": "alice", "container": "box-a", "state": "FINDING_STATE_OPEN", "count": "3",
			 "lastSeen": "2026-10-20T10:00:00Z", "subject": "name:xmrig",
			 "response": {"action": "PROCESS_ACTION_ALERT", "state": "RESPONSE_STATE_NONE",
			              "processKilled": true, "killedPid": 25872, "kills": 3}},
			{"id": "8", "rule": "THREAT_RULE_ID_MINER_SIGNATURE", "severity": "THREAT_SEVERITY_HIGH",
			 "tenantId": "alice", "container": "box-b", "state": "FINDING_STATE_OPEN", "count": "1",
			 "lastSeen": "2026-10-20T10:01:00Z", "subject": "name:xmrig",
			 "response": {"action": "PROCESS_ACTION_THROTTLE", "state": "RESPONSE_STATE_APPLIED",
			              "killSkipReason": "KILL_SKIP_REASON_CONTAINER_INIT"}},
			{"id": "9", "rule": "THREAT_RULE_ID_MINER_SIGNATURE", "severity": "THREAT_SEVERITY_HIGH",
			 "tenantId": "alice", "container": "box-c", "state": "FINDING_STATE_OPEN", "count": "1",
			 "lastSeen": "2026-10-20T10:02:00Z", "subject": "name:xmrig",
			 "response": {"action": "PROCESS_ACTION_ALERT", "state": "RESPONSE_STATE_NONE"}}
		]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(canned))
	}))
	defer srv.Close()

	oldServer := serverAddr
	serverAddr = srv.URL
	defer func() { serverAddr = oldServer }()

	cmd := testCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := runSecurityFindingsList(cmd, nil); err != nil {
		t.Fatalf("runSecurityFindingsList: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"response ALERT NONE, process killed (pid 25872, kills 3)",
		"response THROTTLE APPLIED, kill skipped: CONTAINER_INIT",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "response ALERT NONE\n") != 1 {
		t.Errorf("a response with no kill facts should print bare:\n%s", out)
	}

	// --json prints the gateway's protojson: the kill facts survive the round
	// trip into the generated types, and absent facts stay absent.
	resp, raw := findingsListJSON(t, cmd)
	got := resp.GetFindings()
	if len(got) != 3 || got[0].GetResponse().GetKilledPid() != 25872 || got[0].GetResponse().GetKills() != 3 ||
		got[1].GetResponse().GetKillSkipReason() != pb.KillSkipReason_KILL_SKIP_REASON_CONTAINER_INIT {
		t.Errorf("--json lost the kill facts: %s", raw)
	}
	if strings.Count(raw, `"killedPid"`) != 1 || strings.Count(raw, `"killSkipReason"`) != 1 {
		t.Errorf("--json should omit absent kill facts: %s", raw)
	}
}

// findingsListJSON runs the list with --json and decodes what it printed.
func findingsListJSON(t *testing.T, cmd *cobra.Command) (*pb.ListFindingsResponse, string) {
	t.Helper()
	old := findingsJSONOut
	findingsJSONOut = true
	defer func() { findingsJSONOut = old }()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := runSecurityFindingsList(cmd, nil); err != nil {
		t.Fatalf("runSecurityFindingsList --json: %v", err)
	}
	var resp pb.ListFindingsResponse
	if err := protojson.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("--json output is not the ListFindingsResponse protojson: %v\n%s", err, buf.String())
	}
	return &resp, buf.String()
}

// #2820: a finding shared by two boxes shows which box its response is
// about. Box A was released while box B is still throttled: the line reads
// B's APPLIED, never RELEASED, and --json carries the box.
func TestRunSecurityFindingsList_SharedFindingNamesTheBox(t *testing.T) {
	const canned = `{
		"findings": [
			{"id": "6", "rule": "THREAT_RULE_ID_MINER_SIGNATURE", "severity": "THREAT_SEVERITY_HIGH",
			 "tenantId": "acme", "container": "box-a-container", "state": "FINDING_STATE_OPEN", "count": "2",
			 "lastSeen": "2026-10-20T03:22:13Z", "subject": "name:xmrig",
			 "response": {"action": "PROCESS_ACTION_THROTTLE", "state": "RESPONSE_STATE_APPLIED",
			              "container": "box-b-container"}}
		]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
