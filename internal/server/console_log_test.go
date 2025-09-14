package server

import (
	"context"
	"errors"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestGetConsoleLog_ReturnsLogForVMInstance(t *testing.T) {
	mock := incustest.NewMockBackend()
	mock.GetConsoleLogFunc = func(name string) (string, error) {
		if name != "alice-container" {
			t.Fatalf("unexpected container name: %s", name)
		}
		return "BIOS POST...\nboot hung here\n", nil
