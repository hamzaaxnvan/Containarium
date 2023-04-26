//go:build !windows && !containarium_client

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// #2415 — `containarium sentinel ssh-sessions ship`.

type ingestStub struct {
	mu   sync.Mutex
	auth []string
	reqs []*pb.IngestSSHSessionRecordsRequest
	seen map[string]bool
	srv  *httptest.Server
	fail bool
}

func newIngestStub(t *testing.T) *ingestStub {
	t.Helper()
	s := &ingestStub{seen: map[string]bool{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path != "/v1/audit/ssh-sessions/ingest" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if s.fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var req pb.IngestSSHSessionRecordsRequest
		if err := protojson.Unmarshal(b, &req); err != nil {
			t.Errorf("stub: bad body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.reqs = append(s.reqs, &req)
		resp := &pb.IngestSSHSessionRecordsResponse{}
		for _, rec := range req.Records {
			k := rec.SessionId + "|" + rec.Phase.String() + "|" + rec.CloseReason.String()
			if s.seen[k] {
				resp.Outcomes = append(resp.Outcomes, pb.IngestOutcome_INGEST_OUTCOME_DUPLICATE)
				resp.Duplicates++
			} else {
				s.seen[k] = true
				resp.Outcomes = append(resp.Outcomes, pb.IngestOutcome_INGEST_OUTCOME_INSERTED)
				resp.Inserted++
			}
		}
		out, _ := protojson.Marshal(resp)
		_, _ = w.Write(out)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *ingestStub) records() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.reqs {
		n += len(r.Records)
	}
	return n
}

type shipFixture struct {
	dir     string
	records string
	ckpt    string
	stub    *ingestStub
}

func newShipFixture(t *testing.T) *shipFixture {
	t.Helper()
	dir := t.TempDir()
	return &shipFixture{
		dir: dir, records: filepath.Join(dir, "ssh-sessions.jsonl"),
		ckpt: filepath.Join(dir, "ckpt", "checkpoint.json"), stub: newIngestStub(t),
	}
}

func (f *shipFixture) write(t *testing.T, recs ...sshsession.Record) {
	t.Helper()
	fh, err := os.OpenFile(f.records, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fh.Close() }()
	for _, r := range recs {
		b, _ := json.Marshal(r)
		_, _ = fh.Write(append(b, '\n'))
	}
}

func shipRec(id string, phase sshsession.SessionPhase, at time.Time) sshsession.Record {
	r := sshsession.Record{SessionID: id, Phase: phase, OccurredAt: at, ClientIP: "203.0.113.1", Login: "alice",
		AuthMethod: sshsession.AuthMethodPublicKey, Credential: sshsession.Credential{KeyFingerprint: "SHA256:x"}}
	if phase == sshsession.SessionPhaseClose {
		r.CloseReason = sshsession.CloseReasonNormal
	}
	return r
}

// runShip runs `ship` through cobra with args, so flag wiring is exercised.
func runShip(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{Use: "x"}) // keep the tree non-trivial
	var out bytes.Buffer
	c := newSentinelSSHSessionsShipCmd()
	root.AddCommand(c)
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"ship"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func (f *shipFixture) baseArgs() []string {
	return []string{"--records-file", f.records, "--checkpoint-file", f.ckpt, "--url", f.stub.srv.URL, "--token", "tok-1"}
}

func TestShipCmd_OnceShipsFixtureAndRerunAddsNothing(t *testing.T) {
	f := newShipFixture(t)
	at := time.Now().Add(-time.Hour)
	f.write(t, shipRec("s1", sshsession.SessionPhaseOpen, at), shipRec("s2", sshsession.SessionPhaseOpen, at), shipRec("s1", sshsession.SessionPhaseClose, at))

	out, err := runShip(t, append(f.baseArgs(), "--once")...)
	if err != nil {
