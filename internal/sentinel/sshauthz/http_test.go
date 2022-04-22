package sshauthz

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestHTTPAuthorizerSignedContract(t *testing.T) {
	secret := []byte("test-sentinel-secret-32-bytes-long")
	verifier := auth.NewSentinelVerifier(nil, secret)
	for reasonNumber := int32(0); reasonNumber <= 7; reasonNumber++ {
		t.Run(pb.SSHDenyReason(reasonNumber).String(), func(t *testing.T) {
			reason := pb.SSHDenyReason(reasonNumber)
			response := &pb.AuthorizeSSHResponse{Decision: pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY, DenyReason: reason, UserId: "user-test"}
			if reasonNumber == 0 {
				response.Decision = pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_ALLOW
				response.CacheTtl = durationpb.New(17 * time.Second)
			}
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != AuthorizePath || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("bad request: %s %s", r.Method, r.URL)
				}
				if err := verifier.VerifyRequest(r, time.Now()); err != nil {
					t.Errorf("signature: %v", err)
					w.WriteHeader(401)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("request body: %v", err)
				}
				req := &pb.AuthorizeSSHRequest{}
				if err := protojson.Unmarshal(body, req); err != nil || req.GetLogin() != "alice" || req.GetCredential().GetPublicKey().GetFingerprintSha256() != "SHA256:test" {
					t.Errorf("request: %v %v", req, err)
				}
				raw, err := protojson.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = w.Write(raw)
			}))
			defer daemon.Close()
			a := &HTTPAuthorizer{BackendURL: func(id string) (string, error) {
				if id != "backend" {
					t.Errorf("backend %s", id)
				}
				return daemon.URL, nil
			}, Sign: func(r *http.Request) { auth.SignSentinelRequest(r, secret) }}
			got, err := a.Authorize(context.Background(), "backend", &pb.AuthorizeSSHRequest{Login: "alice", Credential: &pb.SSHCredential{Kind: &pb.SSHCredential_PublicKey{PublicKey: &pb.SSHPublicKeyCredential{FingerprintSha256: "SHA256:test", KeyType: pb.SSHKeyType_SSH_KEY_TYPE_ED25519}}}})
			if err != nil || got.GetDecision() != response.GetDecision() || got.GetDenyReason() != reason || got.GetUserId() != "user-test" || got.GetCacheTtl().AsDuration() != response.GetCacheTtl().AsDuration() {
				t.Fatalf("response: %v %v", got, err)
			}
		})
	}
}

