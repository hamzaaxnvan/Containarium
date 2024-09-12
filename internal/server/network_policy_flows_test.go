package server

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/netbpf"
)

func beIPv4(a, b, c, d byte) uint32 {
	return binary.NativeEndian.Uint32([]byte{a, b, c, d})
}

func TestFlowsToEBPF_AttributesAndMaps(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	records := []netbpf.FlowRecord{
		{ // managed veth 59 -> web-container
			Ifindex:   59,
			Saddr:     beIPv4(10, 100, 0, 42),
			Daddr:     beIPv4(1, 1, 1, 1),
