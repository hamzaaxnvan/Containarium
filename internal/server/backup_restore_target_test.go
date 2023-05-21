package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/backup"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// restoreTargetOps records which containers a restore touches.
type restoreTargetOps struct{ touched map[string]int }

func (o *restoreTargetOps) Exec(c string, _ []string) error { o.touched[c]++; return nil }
func (o *restoreTargetOps) ExecWithOutput(c string, _ []string) (string, string, error) {
	o.touched[c]++
	return "", "", nil
}
func (o *restoreTargetOps) ReadFile(string, string) ([]byte, error) { return nil, os.ErrNotExist }
func (o *restoreTargetOps) WriteFile(c, _ string, _ []byte, _ string) error {
	o.touched[c]++
	return nil
}

func (o *restoreTargetOps) ReadFileStream(string, string) (io.ReadCloser, error) {
	return nil, os.ErrNotExist
}
func (o *restoreTargetOps) WriteFileStream(c, path string, content io.Reader, mode string) error {
	if _, err := io.Copy(io.Discard, content); err != nil {
		return err
	}
	return o.WriteFile(c, path, nil, mode)
}
