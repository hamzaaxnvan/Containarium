package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/anonbox"
	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type fakeAnonDoor struct {
	cfg    anonbox.DoorConfig
	err    error
	setErr error
}

func (f *fakeAnonDoor) DoorConfig() anonbox.DoorConfig           { return f.cfg }
func (f *fakeAnonDoor) SetDoorConfig(c anonbox.DoorConfig) error { f.cfg = c; return f.setErr }
func (f *fakeAnonDoor) DoorErr() error                           { return f.err }

func TestDoorConfig_GetAndSet(t *testing.T) {
