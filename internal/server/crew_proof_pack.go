package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/proofpack"
	"github.com/footprintai/containarium/internal/runlease"
	containerpkg "github.com/footprintai/containarium/pkg/core/container"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// A crew's PACK_PRODUCER (#2799) is a box, not an agent. The daemon
// provisions it with the run's checkout and no credentials, writes the crew's
// final files into that checkout once the last A2A member returns them, and
// runs the repository's test commands there with proofpack.Generate. Nothing
// in the pack comes from the coder's box or from any agent's own claim.

// packBoxOps is the part of *container.Manager producing a pack goes through.
type packBoxOps interface {
	proofpack.Execer
	Exec(container string, cmd []string) error
	WriteFile(container, path string, content []byte, mode string) error
}

var _ packBoxOps = (*containerpkg.Manager)(nil)

// crewFileArtifact is the part of a coding crew's final artifact the producer
// needs: the whole content of every changed file (cloud#1738's shape, which
// diff-reviewer and issue-implementer both emit).
type crewFileArtifact struct {
	Files []crewArtifactFile `json:"files"`
}

type crewArtifactFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// parseArtifactFiles reads the files out of a final artifact and refuses any
// path that is not inside the checkout. The whole artifact is refused, so a
// pack never tests a partial write.
func parseArtifactFiles(artifactJSON string) ([]crewArtifactFile, error) {
	var a crewFileArtifact
	if err := json.Unmarshal([]byte(artifactJSON), &a); err != nil {
		return nil, fmt.Errorf("final artifact is not a files artifact: %w", err)
	}
	for _, f := range a.Files {
		if !filepath.IsLocal(f.Path) || strings.Contains(f.Path, `\`) {
			return nil, fmt.Errorf("final artifact names path %q, which is not inside the checkout", f.Path)
		}
	}
	return a.Files, nil
}

// produceProofPack writes the artifact's files into repoDir in box, then
// generates the pack there. A path the checkout itself redirects (a symlink
// committed to the repository) is followed inside this box only, which runs
// the repository's commands as root anyway.
func produceProofPack(ctx context.Context, ops packBoxOps, box, repoDir, artifactJSON string) (*pb.ProofPack, error) {
	files, err := parseArtifactFiles(artifactJSON)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		dst := path.Join(repoDir, f.Path)
		if err := ops.Exec(box, []string{"mkdir", "-p", "--", path.Dir(dst)}); err != nil {
			return nil, fmt.Errorf("create %s in %s: %w", path.Dir(dst), box, err)
		}
		if err := ops.WriteFile(box, dst, []byte(f.Content), "0644"); err != nil {
			return nil, fmt.Errorf("write %s in %s: %w", f.Path, box, err)
		}
	}
