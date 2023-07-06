package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/footprintai/containarium/internal/alert"
	"github.com/footprintai/containarium/internal/coderun/engine"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// AgentSkillServer is the credential-expiry watcher's CredentialProber
// (#2371): it lists the skill boxes worth probing and probes one through the
// watcher policy probe; the manual RPC continues to show raw token expiry.

// CredentialWatchTargets lists every running skill box whose skill is in the
// catalog and whose engine has a credential-status probe. A stopped box is
// skipped (it cannot be exec'd into) and is picked up again once it runs.
func (s *AgentSkillServer) CredentialWatchTargets(context.Context) ([]alert.CredentialWatchTarget, error) {
	if s.recipes == nil || s.recipes.containers == nil || s.recipes.containers.manager == nil {
		return nil, nil
