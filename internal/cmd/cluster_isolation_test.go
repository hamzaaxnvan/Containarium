package cmd

// CLI half of the node isolation contract (#1428): --isolation is an
// enum-backed flag (a typo is rejected at parse time, never sent to the
// daemon as a silent default), and every cluster read surface prints
// the class an operator would be audited on.

import (
	"bytes"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestIsolationFlag_ParsesEnumValuesOnly(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    pb.NodeIsolation
		wantErr bool
	}{
		{"vm", "vm", pb.NodeIsolation_NODE_ISOLATION_VM, false},
		{"container", "container", pb.NodeIsolation_NODE_ISOLATION_CONTAINER, false},
