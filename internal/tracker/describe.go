package tracker

import (
	"context"
	"errors"
	"time"
)

// Conn carries what a provider adapter needs to reach the tracker: base
// URL, project, and the resolved credential. Built per call and never
// stored or logged — see docs/architecture/agent-tracker-broker.md.
type Conn struct {
	// BaseURL is empty for the provider's SaaS endpoint (github.com /
	// gitlab.com), or set for GitHub Enterprise Server / self-managed
	// GitLab.
	BaseURL string
	// Project is "owner/repo" (GitHub) or "group/subgroup/project"
	// (GitLab). Unused by DescribeCredential today but carried here
	// since every other adapter verb (#1922) needs it and Conn is the
	// one seam constructed at every call site.
	Project string
	// Credential is the plaintext broker-only secret value, resolved via
	// CredentialSource immediately before the call.
	Credential string
}

// CredentialBreadth classifies a credential against the provider's
// preferred (narrowest) type — see the design note's "Preferred
// credential types" table. It bounds what a DAEMON compromise can do,
// as distinct from the broker's verb set, which bounds what a BOX can
// do.
type CredentialBreadth int

const (
	// BreadthUnspecified means breadth could not be determined — the
	// probe that would determine it failed (e.g. the tracker was
	// unreachable) before classification was possible.
	BreadthUnspecified CredentialBreadth = iota
	// BreadthPreferred is the narrowest type for the provider: a GitLab
	// project access token, or a GitHub App installation token /
	// single-repository fine-grained PAT.
	BreadthPreferred
	// BreadthBroad is anything wider: a GitLab group or personal access
	// token, or a GitHub classic PAT.
	BreadthBroad
)

func (b CredentialBreadth) String() string {
	switch b {
	case BreadthPreferred:
		return "preferred"
	case BreadthBroad:
		return "broad"
	default:
		return "unspecified"
	}
}

// CredentialKind is the provider-native kind of a credential, when the
// provider's own introspection reveals it. It complements
// CredentialBreadth: breadth ranks a credential against the preferred
// type, kind says which type it is (a GitLab group token and a personal
// token are both broad, but are different kinds).
type CredentialKind int

const (
	// KindUnspecified means the kind could not be determined: the probe
	// that would classify it failed, or the provider's introspection does
	// not reveal it for this credential.
	KindUnspecified CredentialKind = iota
	// KindProject is a GitLab project access token.
	KindProject
	// KindGroup is a GitLab group access token.
	KindGroup
	// KindPersonal is a credential owned by a real user account: a
	// GitLab personal access token or a GitHub classic PAT.
	KindPersonal
)

func (k CredentialKind) String() string {
	switch k {
	case KindProject:
		return "project"
	case KindGroup:
		return "group"
	case KindPersonal:
		return "personal"
	default:
		return "unspecified"
	}
