package netpolicy

import (
	"fmt"
	"slices"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// defaultEgressDomains is the built-in part of the default egress allow-list
// (#2577, stage 1 of the staged default-deny decision): git hosts and the
// package registries of the supported base images. DNS is not listed; the
// ALLOW_LIST_ONLY preset allows the box's resolver implicitly. The model
// gateway and the control plane are deployment-specific, so the operator adds
// them through DefaultEgressExtras.
//
// This is a starting point for the soak, not a final list: stage 2 reviews the
// logged denials (network_policy.deny_logged) and fixes this list, not the
// tenant.
var defaultEgressDomains = []string{
	// git hosts
	"github.com", "api.github.com", "codeload.github.com", "gitlab.com", "bitbucket.org",
	// language registries
	"registry.npmjs.org", "pypi.org", "files.pythonhosted.org",
