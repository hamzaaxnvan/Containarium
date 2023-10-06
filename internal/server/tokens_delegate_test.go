package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ExchangeDelegatedToken exists to let a fronting service act for a user
// without holding a signing key (containarium-cloud#1427). Its safety rests
// entirely on two invariants, so those are what these tests pin.

func delegateTestServer(t *testing.T) *TokensServer {
	t.Helper()
	tm, err := auth.NewTokenManager(delegateTestSecret, "containarium-test")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	return NewTokensServer(tm, nil, 0)
}

const delegateTestSecret = "test-secret-for-delegation-tests-0123456789"

// callerCtx builds an authenticated caller carrying both a scopes claim and
// an act chain — the shape the gateway annotator produces in production. The
// two ContextWithTestSubject* helpers each write a full metadata set, so the
// scopes pair is merged onto the act-bearing context rather than layered by a
// second constructor call, which would drop the act.
func callerCtx(username string, scopes []string, act *auth.Actor) context.Context {
	ctx := auth.ContextWithTestSubjectAct(context.Background(), username, []string{"admin"}, act)
	if scopes == nil {
		return ctx
	}
	md, _ := metadata.FromIncomingContext(ctx)
	md = md.Copy()
	md.Set(auth.MDKeyScopes, strings.Join(scopes, ","))
	return metadata.NewIncomingContext(ctx, md)
}

// THE invariant: an exchange can never produce more authority than the caller
// already holds. Without it this endpoint is #1676's escalation reopened.
func TestExchangeDelegatedToken_CannotWidenAuthority(t *testing.T) {
	s := delegateTestServer(t)
	ctx := callerCtx("cloud-daemon",
		[]string{auth.ScopeTokensDelegate, auth.ScopeContainersRead},
		nil)

	resp, err := s.ExchangeDelegatedToken(ctx, &pb.ExchangeDelegatedTokenRequest{
		Subject: "alice@example.com",
		// Asking for far more than the caller holds.
		Scopes: []string{auth.ScopeContainersRead, auth.ScopeSecretsRead, auth.ScopeContainersWrite},
	})
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	for _, s := range resp.GetGrantedScopes() {
		if s == auth.ScopeSecretsRead || s == auth.ScopeContainersWrite {
			t.Fatalf("granted %q — the caller does not hold it; the exchange widened authority", s)
		}
	}
	if len(resp.GetGrantedScopes()) != 1 || resp.GetGrantedScopes()[0] != auth.ScopeContainersRead {
		t.Errorf("granted = %v, want only containers:read", resp.GetGrantedScopes())
	}
}

// act must come from the authenticated context, never from the request — the
// rule #1677 states. A caller that could name its own actor could forge the
// audit trail.
func TestExchangeDelegatedToken_ActIsServerSetAndNests(t *testing.T) {
	s := delegateTestServer(t)
	// The caller itself already acts for someone: a two-hop chain.
	existing := &auth.Actor{Subject: "human@example.com"}
	ctx := callerCtx("cloud-daemon", []string{auth.ScopeTokensDelegate}, existing)

	resp, err := s.ExchangeDelegatedToken(ctx, &pb.ExchangeDelegatedTokenRequest{
		Subject: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	tm, _ := auth.NewTokenManager(delegateTestSecret, "containarium-test")
	claims, err := tm.ValidateToken(resp.GetToken())
	if err != nil {
		t.Fatalf("minted token does not validate: %v", err)
	}
	if claims.Username != "alice@example.com" {
		t.Errorf("subject = %q, want the delegated-for user", claims.Username)
	}
	if claims.Act == nil {
		t.Fatal("no act chain — the delegation path was not recorded")
	}
	if claims.Act.Subject != "cloud-daemon" {
		t.Errorf("act.sub = %q, want the authenticated caller", claims.Act.Subject)
	}
	// The caller's own chain must be WRAPPED, not dropped: RootActor is what
	// #1678's audit `actor` column resolves to, and flattening here would
	// lose the human furthest from the leaf.
	if got := auth.RootActor(claims.Act); got != "human@example.com" {
		t.Errorf("RootActor = %q, want the original human — the chain was flattened", got)
	}
}

func TestExchangeDelegatedToken_RequiresDelegateScope(t *testing.T) {
	s := delegateTestServer(t)
	// tokens:write is deliberately NOT enough: managing your own tokens and
	// acting as someone else are different capabilities.
	ctx := callerCtx("cloud-daemon", []string{auth.ScopeTokensWrite}, nil)

