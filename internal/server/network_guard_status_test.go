package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/coreguard"
	"github.com/footprintai/containarium/internal/nicguard"
	"github.com/footprintai/containarium/internal/tenantguard"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestTenantGuardStatusToProto(t *testing.T) {
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	st := tenantguard.Status{
		Mode:           nicguard.ModeEnforce,
		FirewallDriver: "nftables",
		Entries: []tenantguard.Entry{
			{Container: "alice-container", Tenant: "alice", IP: "10.100.0.17", ACLName: "containarium-tenant-abc", Attached: true},
			{Container: "cld-1", Tenant: "org-a", LastError: "nic device: boom"},
		},
		Unresolved: []string{"mystery"},
		Unmanaged:  []string{"hand-made"},
		Tenants:    2,
		LastPass:   at,
