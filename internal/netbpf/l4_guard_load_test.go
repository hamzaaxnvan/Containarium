//go:build ebpf_load

package netbpf

import (
	"fmt"
	"net/netip"
	"os/exec"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #3166: the virtual-patch deny (#660) and per-flow accounting (#627) share
// parse_l4's guard — a packet whose IP header carries options (ihl != 5) or that
// is a fragment has no trustworthy L4 ports. These tests pin, on a real kernel:
//
//   - TestAttachedProgram_PortScopedDenyNotEvadedByIPOptions: a port-scoped deny
//     cannot be evaded with IP options or fragmentation — an unknown port on a
//     port-carrying protocol fails closed (the rule's address and proto decide).
//   - TestAttachedProgram_PortScopedUDPDenyNotEvadedByIPOptions: the same for
//     a UDP rule.
//   - TestAttachedProgram_FragmentsAreNotAccounted: fragments are skipped by the
//     flow accounting on the sender hook (not tallied under port 0); an
//     options-bearing non-fragment is tallied under ports 0, like any packet
//     whose ports are unknown.
//   - TestAttachedProgram_ReplyFragmentsAreNotAccounted: the same for reply
//     accounting on the veth egress hook.

const (
	l4GuardDenyPort  = 8690 // the deny rule's port
	l4GuardOtherPort = 8691 // same host, not denied
	l4GuardSport     = 40690
	l4GuardUDPSport  = 4242
	l4GuardUDPDport  = 4343
)

// sendCraftedScript sends three IPv4 packets with a hand-built header from a raw
// socket, so the test controls ihl and frag_off. argv: src dst proto(tcp|udp)
// sport dport options(0|1) frag(none|mf|offset) payload. options=1 adds one
// 32-bit word of NOP/EOL options (ihl=6); frag=mf sets More-Fragments at offset
// 0 (a first fragment, L4 header present); frag=offset sets a non-zero fragment
// offset (a later fragment); frag=trunc sends an unfragmented packet whose L4
// header is cut after 4 bytes (a truncated header). An optional 9th argument
// names an interface to inject the frame onto directly (AF_PACKET).
const sendCraftedScript = `
import socket, struct, sys
src, dst, proto, sport, dport, opt, frag, payload = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5]), sys.argv[6] == "1", sys.argv[7], sys.argv[8].encode()
def csum(b):
    if len(b) % 2:
        b += b"\0"
    s = sum(struct.unpack("!%dH" % (len(b) // 2), b))
    s = (s >> 16) + (s & 0xffff)
    s += s >> 16
    return (~s) & 0xffff
if proto == "tcp":
    pnum = 6
    l4 = struct.pack("!HHIIBBHHH", sport, dport, 1000, 0, 5 << 4, 0x02, 65535, 0, 0)  # SYN
    pseudo = socket.inet_aton(src) + socket.inet_aton(dst) + struct.pack("!BBH", 0, pnum, len(l4) + len(payload))
    l4 = l4[:16] + struct.pack("!H", csum(pseudo + l4 + payload)) + l4[18:]
else:
    pnum = 17
    l4 = struct.pack("!HHHH", sport, dport, 8 + len(payload), 0)
opts = b"\x01\x01\x01\x00" if opt else b""
ihl = 5 + len(opts) // 4
if frag == "trunc":
    # An unfragmented packet cut inside its L4 header: the ports are not in the
    # linear data, so parse_l4 reports them unknown.
    l4 = l4[:4]
    payload = b""
    frag = "none"
frag_off = {"none": 0, "mf": 0x2000, "offset": 1}[frag]
ip = struct.pack("!BBHHHBBH4s4s", (4 << 4) | ihl, 0, 20 + len(opts) + len(l4) + len(payload), 0x3166, frag_off, 64, pnum, 0,
                 socket.inet_aton(src), socket.inet_aton(dst)) + opts
iface = sys.argv[9] if len(sys.argv) > 9 else ""
if iface:
    # Inject the frame straight onto the veth with an AF_PACKET socket, below
    # netfilter: a locally-sent fragment would otherwise be swallowed by
    # conntrack's defragmentation before it reaches the TC egress hook.
    s = socket.socket(socket.AF_PACKET, socket.SOCK_RAW)
    s.bind((iface, 0))
    eth = b"\x02\x00\x00\x00\x31\x66" + b"\x02\x00\x00\x00\x31\x67" + b"\x08\x00"
    for _ in range(3):
        s.send(eth + ip + l4 + payload)
else:
    s = socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_RAW)
    s.setsockopt(socket.IPPROTO_IP, socket.IP_HDRINCL, 1)
    for _ in range(3):
        s.sendto(ip + l4 + payload, (dst, 0))
`

// craftedPacket describes one sendCraftedScript invocation from the box netns
// to the host-side address.
type craftedPacket struct {
	proto   string // "tcp" | "udp" (the script's argv vocabulary)
	sport   int
	dport   int
	options bool
	frag    string // "none" | "mf" | "offset" | "trunc"
}

func sendCrafted(t *testing.T, p craftedPacket) {
	t.Helper()
	runCrafted(t, p, ebpfLoadPeerAddr, ebpfLoadHostAddr, "", "ip", "netns", "exec", ebpfLoadNetns)
}

// sendCraftedToBox sends p from the host namespace to the box, so it crosses the
// host veth's TC egress hook (the reply direction).
func sendCraftedToBox(t *testing.T, p craftedPacket) {
	t.Helper()
	runCrafted(t, p, ebpfLoadHostAddr, ebpfLoadPeerAddr, ebpfLoadHostVeth)
}

func runCrafted(t *testing.T, p craftedPacket, src, dst, iface string, prefix ...string) {
	t.Helper()
	opt := "0"
	if p.options {
		opt = "1"
	}
	argv := append(append([]string{}, prefix...), "python3", "-c", sendCraftedScript,
		src, dst, p.proto, fmt.Sprint(p.sport), fmt.Sprint(p.dport), opt, p.frag, "l4-guard-3166", iface)
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput() // #nosec G204 -- fixed argv, constant script
	if err != nil {
		t.Fatalf("send crafted %+v %s -> %s: %v\n%s", p, src, dst, err, out)
	}
}

