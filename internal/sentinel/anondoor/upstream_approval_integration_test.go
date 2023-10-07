//go:build integration

package anondoor

import (
	"github.com/footprintai/containarium/internal/sentinel/sshcontrol"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestIntegration_PublicKeyApprovalThroughTransportAdapter reruns the established
// signature/partial-success/approval assertions through the new routing hook.
func TestIntegration_PublicKeyApprovalThroughTransportAdapter(t *testing.T) {
	adapter := os.Getenv("SSH_BAN_DAEMON")
