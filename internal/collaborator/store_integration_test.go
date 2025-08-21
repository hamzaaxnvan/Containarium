//go:build integration

// Integration coverage for the collaborator store (#1300).
//
// This store is access control. A row grants a named user SSH access to
// someone else's container, and carries the privilege flags — sudo, container
// runtime — that decide what they can do once inside. None of its SQL had ever
// been executed by a test.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/collaborator/
package collaborator

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func collabStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Fatal("CONTAINARIUM_TEST_DSN is unset. Failing rather than skipping — this is access " +
			"control, and a skipped test looks exactly like a passing one.")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `DROP TABLE IF EXISTS collaborators`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	pool.Close()

	s, err := NewStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func add(t *testing.T, s *Store, container, owner, user string, sudo bool) {
	t.Helper()
	err := s.Add(context.Background(), &Collaborator{
		ContainerName:        container,
		OwnerUsername:        owner,
		CollaboratorUsername: user,
		AccountName:          container + "-" + user,
		SSHPublicKey:         "ssh-ed25519 AAAA" + user,
		CreatedBy:            owner,
		HasSudo:              sudo,
