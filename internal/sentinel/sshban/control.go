package sshban

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultSocket isolates control endpoints for the two independently enforcing
// processes. No remote request can choose a filesystem path.
func DefaultSocket(listener pb.SSHBanListener) string {
	switch listener {
	case pb.SSHBanListener_SSH_BAN_LISTENER_MAIN:
		return "/run/containarium/ssh-ban/main.sock"
	case pb.SSHBanListener_SSH_BAN_LISTENER_ANONYMOUS:
		return "/run/containarium/ssh-ban/anonymous.sock"
	default:
		return ""
