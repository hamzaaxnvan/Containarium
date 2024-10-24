package sshsession

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/proto"
)

type authorizationCaptureClient struct{ records []*pb.SSHSessionRecord }

