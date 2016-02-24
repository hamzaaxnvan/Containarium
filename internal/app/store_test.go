package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Note: These tests require a running PostgreSQL instance.
// For unit testing without external dependencies, we'd need to mock the pgxpool.
// For now, these are integration-style tests that can be run with:
// docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=test -e POSTGRES_DB=containarium postgres:16-alpine

func createTestApp(username, name string) *v1.App {
	now := timestamppb.Now()
	return &v1.App{
		Id:             uuid.New().String(),
		Name:           name,
		Username:       username,
		ContainerName:  username + "-container",
		Subdomain:      name,
		FullDomain:     name + ".containarium.dev",
		Port:           3000,
		State:          v1.AppState_APP_STATE_RUNNING,
		ContainerImage: username + "/" + name + ":latest",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestStoreOperations(t *testing.T) {
