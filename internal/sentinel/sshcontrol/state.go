package sshcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/footprintai/containarium/internal/sentinel/sshgate"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// policyHash binds restored history to the exact operator policy and inventory.
func (c *Controller) policyHash() string {
	data, _ := proto.MarshalOptions{Deterministic: true}.Marshal(c.cfg.Policy)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// privateDirectory refuses symlinks and accessible final state/socket directories.
func privateDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("private upstream path must be absolute")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	for dir := path; dir != "/"; dir = filepath.Dir(dir) {
		st, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || (dir == path && st.Mode().Perm()&0077 != 0) {
			return fmt.Errorf("unsafe private upstream directory: %s", dir)
		}
	}
	return nil
}

// restore rejects incompatible/corrupt history instead of granting fresh budget.
func (c *Controller) restore() error {
	if err := privateDirectory(filepath.Dir(c.cfg.StatePath)); err != nil {
		return err
	}
	st, err := os.Lstat(c.cfg.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		c.readyAt = c.cfg.Now().Add(c.cfg.Policy.Window.AsDuration())
		return c.persist()
	}
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 16<<20 {
		return errors.New("unsafe upstream checkpoint")
	}
	data, err := os.ReadFile(c.cfg.StatePath)
	if err != nil {
		return err
	}
