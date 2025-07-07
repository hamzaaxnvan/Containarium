package server

import (
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// TestBuildContainerIPMap covers the source-IP → identity projection that
// feeds container_ips.json (#231/#264 metrics ingest). Each entry must carry
// the cloud_container_id (the cloud's metrics join key) when present; core
// containers and IP-less boxes are excluded.
func TestBuildContainerIPMap(t *testing.T) {
