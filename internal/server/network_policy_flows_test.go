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
			Sport:     51000,
			Dport:     443,
			Proto:     6,
			Packets:   12,
			Bytes:     8456,
			RxPackets: 9,
			RxBytes:   12_004,
			FirstNs:   1_000_000_000,
			LastNs:    3_000_000_000, // 2s old
		},
		{ // unmanaged veth 99 -> dropped
			Ifindex: 99,
			Saddr:   beIPv4(10, 100, 0, 99),
			Daddr:   beIPv4(8, 8, 8, 8),
			Proto:   17,
			Packets: 1,
			Bytes:   60,
		},
	}
	attached := map[int]string{59: "web-container"}

	out := flowsToEBPF(records, attached, now)
