package sentinel

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// servedVersionResponse is the /containarium/version body: the version of the
// binary this sentinel serves at /containarium, plus the checksum it was read
// from so a caller can tie the two together. #2171.
type servedVersionResponse struct {
	Version  string `json:"version"`
	Checksum string `json:"checksum"`
}

// servedVersionCache reports the served binary's own version. It is read from
// the FILE (by running its `version` subcommand), not from this process's
// build info: /sentinel/version reports the running sentinel, which only
// matches the served binary until someone swaps the file without a restart.
// The probe execs a binary, so the result is cached per checksum and probes
// are serialized.
type servedVersionCache struct {
	probe func(ctx context.Context, binaryPath string) (string, error)

	mu       sync.Mutex
	checksum string
	version  string
}

func newServedVersionCache() *servedVersionCache {
	return &servedVersionCache{probe: probeBinaryVersion}
}

func (c *servedVersionCache) get(ctx context.Context, binaryPath string) (servedVersionResponse, error) {
	sum, err := sha256File(binaryPath)
	if err != nil {
		return servedVersionResponse{}, fmt.Errorf("checksum served binary: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if sum != c.checksum {
		v, err := c.probe(ctx, binaryPath)
		if err != nil {
			return servedVersionResponse{}, err
		}
		c.checksum, c.version = sum, v
