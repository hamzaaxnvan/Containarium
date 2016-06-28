package netbpf

import (
	"encoding/binary"
	"fmt"
	"net/netip"
)

// FlowRecord is one decoded per-flow accounting entry from the BPF `flows` map
// (issue #627). It carries the flow's 5-tuple — attributed to a container by
// Ifindex — plus the cumulative tally: Packets/Bytes are the container's EGRESS
// (veth ingress hook), RxPackets/RxBytes the reply direction (veth egress hook,
// #631). The wire layout of the key/value must stay in lockstep with `struct
// flow_key` / `struct flow_stat` in experimental/ebpf-phaseA/netpolicy.bpf.c.
type FlowRecord struct {
	Ifindex   uint32
	Saddr     uint32 // source IPv4, network byte order (as carried on the wire)
	Daddr     uint32 // destination IPv4, network byte order
	Sport     uint16 // host byte order
	Dport     uint16 // host byte order
	Proto     uint8  // IP protocol number (1=ICMP, 6=TCP, 17=UDP)
	Packets   uint64 // tx: container → peer (egress)
	Bytes     uint64
	FirstNs   uint64 // bpf_ktime_get_ns at first packet (monotonic; boot-relative)
	LastNs    uint64 // bpf_ktime_get_ns at most recent packet (monotonic)
	RxPackets uint64 // reply: peer → container (#631); 0 if the object predates it
