package server

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2483: the durable store can now be installed after the server is serving
// (the recovery retry), so the swap has to reach every reader, including the
// Provider() the deploy gate and model gateway captured at startup.

func TestGuardrailPolicyProvider_FollowsDurableInstall(t *testing.T) {
	s := NewGuardrailPolicyServer(guardrailPolicyStartupStore(true, nil))
	provider := s.Provider() // captured at startup, as the gate and gateway do
	if s.Durable() {
