package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// #1001: ForwardCreateContainer hand-copied a subset of the request into a
// map[string]interface{}, silently dropping every field nobody remembered to
// add. Creating on a tunnel-connected backend and creating locally quietly
// meant different things.
//
// The worst of the dropped fields were `encrypted` and `tenant_id`: a
// `create --encrypted` aimed at a remote backend produced an UNENCRYPTED
// container and reported success.

// fullCreateRequest sets every field of CreateContainerRequest to a non-zero
// value, so "was anything dropped?" can be asked of the whole message rather
// than of a list someone has to maintain.
func fullCreateRequest() *pb.CreateContainerRequest {
	return &pb.CreateContainerRequest{
		Username:                  "alice",
		Resources:                 &pb.ResourceLimits{Cpu: "4", Memory: "4GB", Disk: "50GB"},
		SshKeys:                   []string{"ssh-ed25519 AAAA test"},
		Labels:                    map[string]string{"team": "platform"},
		Image:                     "ubuntu:24.04",
		EnablePodman:              true,
		Async:                     true,
		StaticIp:                  "10.0.0.5",
		Stack:                     "python",
		Gpu:                       "gpu0",
		BackendId:                 "peer-a",
		OsType:                    pb.OSType_OS_TYPE_UBUNTU_2404,
		StackParameters:           map[string]string{"version": "3.12"},
		Monitoring:                true,
		Pool:                      "gpu-pool",
		GitSource:                 "https://example.com/repo.git",
		GitRef:                    "main",
		GitCredential:             "cred-1",
		WorkspacePath:             "/workspace",
		TtlSeconds:                3600,
		IdleStopMinutes:           30,
		DeleteAfterStoppedSeconds: 7200,
		Gpus:                      []string{"gpu0"},
		Encrypted:                 true,
		TenantId:                  "default",
	}
}

