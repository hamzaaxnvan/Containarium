package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestSendA2ATask exercises the A2A client against a stub peer that speaks the
// AgentTask -> AgentArtifact contract (what the in-box A2A server will do).
func TestSendA2ATask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != a2aTasksPath {
			t.Errorf("unexpected path %q, want %q", r.URL.Path, a2aTasksPath)
		}
		body, _ := io.ReadAll(r.Body)
		task := &pb.AgentTask{}
		if err := protojson.Unmarshal(body, task); err != nil {
			t.Errorf("peer could not decode task: %v", err)
		}
		art := &pb.AgentArtifact{
			TaskId:     task.Id,
			OutputJson: `{"ok":true}`,
			State:      pb.AgentTaskState_AGENT_TASK_STATE_COMPLETED,
		}
		out, _ := protojson.Marshal(art)
		w.Header().Set("Content-Type", "application/json")
