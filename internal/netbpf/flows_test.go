package netbpf

import (
	"encoding/binary"
	"testing"
)

// encodeFlowStat mirrors the C `struct flow_stat` wire layout, so the round-trip
// test pins the byte offsets the loader's Flows() decode relies on. (The key
// side uses the production encodeFlowKey from loader.go, added for DeleteFlow —
// this test doubles as its round-trip coverage.)
func encodeFlowStat(r FlowRecord) []byte {
	b := make([]byte, flowStatSize)
	binary.NativeEndian.PutUint64(b[0:8], r.Packets)
	binary.NativeEndian.PutUint64(b[8:16], r.Bytes)
	binary.NativeEndian.PutUint64(b[16:24], r.FirstNs)
