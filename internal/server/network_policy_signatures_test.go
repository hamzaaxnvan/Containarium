package server

import (
	"bytes"
	"testing"

	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/netpolicy"
)

func TestToSigEntry(t *testing.T) {
	e := toSigEntry(netpolicy.Signature{ID: 5, Name: "log4shell", Pattern: []byte("${jndi:")})
	if e.ID != 5 || !e.Enabled || e.Len != 7 {
		t.Fatalf("entry = %+v, want id=5 enabled len=7", e)
	}
	if string(e.Pattern[:e.Len]) != "${jndi:" {
