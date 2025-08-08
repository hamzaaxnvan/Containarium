//go:build ebpf_load

package netbpf

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2618: the program runs on the box's host veth TC_INGRESS — every packet the
// box sends — and keeps no connection state, so a box's reply to a connection
// someone else opened (a SYN-ACK, an ICMP echo reply) is judged as if the box
// had opened a new flow. These tests pin both halves of a sound answer on a
// real kernel:
//
//   - TestAttachedProgram_InboundConnectionToEnforcedBox: a peer that connects
//     INTO an enforced box with an empty allow-list gets answered.
//   - TestAttachedProgram_EnforcedBoxEgressStaysJudged: what the box opens
//     itself — a TCP dial, or a forged ACK-only segment carrying a payload,
//     which needs no handshake and so would slip past a "judge only SYNs"
//     rule — is still dropped.
//
// Each phase first runs under log_only as a positive control, so an enforce
// failure can only come from the program.
const (
	// The box's listener (in the netns) for inbound connections.
	ebpfLoadInboundPort = 8678
	// A routed peer on the host side that is not on the box's allow-list, with
	// a TCP listener for the box-initiated dial.
	ebpfLoadUnlistedAddr = "10.250.115.1"
	ebpfLoadUnlistedPort = 8679
)

// setEmptyAllowListPolicy compiles the box policy every phase below uses: no
// egress CIDR, no preset, no metadata opt-in.
func setEmptyAllowListPolicy(t *testing.T, loader *Loader, ifindex int, mode pb.NetworkPolicyMode) {
	t.Helper()
	c, err := netpolicy.Compile(&pb.NetworkPolicy{Tenant: "inbound-reply-box", Mode: mode})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if err := loader.SetVethPolicy(ifindex, CompileConfig(ebpfLoadTenant, c)); err != nil {
		t.Fatalf("SetVethPolicy: %v", err)
	}
}

func TestAttachedProgram_InboundConnectionToEnforcedBox(t *testing.T) {
	loader, err := Load(ebpfLoadObjPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer func() { _ = loader.Close() }()

	ifindex, err := VethIndex(ebpfLoadHostVeth)
	if err != nil {
		t.Fatalf("VethIndex(%q): %v", ebpfLoadHostVeth, err)
	}
	setEmptyAllowListPolicy(t, loader, ifindex, pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY)
	if err := loader.AttachVeth(ifindex); err != nil {
		t.Fatalf("AttachVeth: %v", err)
	}
	// The daemon attaches BOTH hooks (reconcile loop); the egress hook is where
	// a flow opened toward the box is recorded (#2618), so the sender hook can
	// recognise the box's answers. Without it the box's reply is judged as a new
	// outbound flow — which is the very bug under test.
	if err := loader.AttachVethEgress(ifindex); err != nil {
		t.Fatalf("AttachVethEgress: %v", err)
	}
	t.Cleanup(func() { _ = loader.DetachVeth(ifindex) })

	// A service in the box: python3's stdlib HTTP server (on the runner image).
	srv := exec.Command("ip", "netns", "exec", ebpfLoadNetns, // #nosec G204 -- fixed argv
		"python3", "-m", "http.server", fmt.Sprint(ebpfLoadInboundPort), "--bind", ebpfLoadPeerAddr)
	if err := srv.Start(); err != nil {
		t.Fatalf("start in-netns listener: %v", err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _ = srv.Wait() })

	url := fmt.Sprintf("http://%s/", net.JoinHostPort(ebpfLoadPeerAddr, fmt.Sprint(ebpfLoadInboundPort)))
	client := &http.Client{Timeout: 3 * time.Second}
	// httpExchange proves the box's SYN-ACK and its data segments both reached
	// the initiator (the host), not just that a handshake started.
	httpExchange := func() error {
		resp, err := client.Get(url)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			return fmt.Errorf("read body: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	}
	hostPing := func() error {
		out, err := exec.Command("ping", "-c", "3", "-i", "0.2", "-W", "1", ebpfLoadPeerAddr).CombinedOutput() // #nosec G204 -- fixed argv
		if err != nil {
			return fmt.Errorf("%w: %s", err, out)
		}
		return nil
	}

	// Positive control (log_only): the listener is up and both probes work.
	var ctrlErr error
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ctrlErr = httpExchange(); ctrlErr == nil {
			break
		}
		time.Sleep(200 * time.Millisecond) // listener still starting
	}
	if ctrlErr != nil {
		t.Fatalf("control (log_only): host -> box HTTP exchange failed with nothing enforced: %v", ctrlErr)
	}
	if err := hostPing(); err != nil {
		t.Fatalf("control (log_only): host -> box ping failed with nothing enforced: %v", err)
	}

	// Enforce with the same empty allow-list. The host initiated both flows;
	// the box only answers.
	setEmptyAllowListPolicy(t, loader, ifindex, pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE)
	if err := httpExchange(); err != nil {
		t.Errorf("enforce: host -> box HTTP exchange failed — the box's reply was judged as a new outbound flow: %v", err)
	}
	if err := hostPing(); err != nil {
		t.Errorf("enforce: host -> box ping got no echo reply — the box's reply was judged as a new outbound flow: %v", err)
	}
}

// forgeACKScript sends three ACK-only TCP segments (no SYN anywhere, no prior
// connection) carrying argv[5] as payload, from inside the box over a raw
// socket. The box is root in its own netns, so it can always do this.
const forgeACKScript = `
import socket, struct, sys
src, dst, sport, dport, payload = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]), sys.argv[5].encode()
def csum(b):
    if len(b) % 2:
        b += b"\0"
    s = sum(struct.unpack("!%dH" % (len(b) // 2), b))
    s = (s >> 16) + (s & 0xffff)
    s += s >> 16
    return (~s) & 0xffff
hdr = struct.pack("!HHIIBBHHH", sport, dport, 1000, 2000, 5 << 4, 0x10, 65535, 0, 0)
pseudo = socket.inet_aton(src) + socket.inet_aton(dst) + struct.pack("!BBH", 0, 6, len(hdr) + len(payload))
c = csum(pseudo + hdr + payload)
hdr = hdr[:16] + struct.pack("!H", c) + hdr[18:]
s = socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_TCP)
for _ in range(3):
    s.sendto(hdr + payload, (dst, 0))
`

func TestAttachedProgram_EnforcedBoxEgressStaysJudged(t *testing.T) {
	loader, err := Load(ebpfLoadObjPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer func() { _ = loader.Close() }()

	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); /* #nosec G204 -- fixed argv */ err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("ip", "addr", "add", ebpfLoadUnlistedAddr+"/32", "dev", ebpfLoadHostVeth)
	run("ip", "netns", "exec", ebpfLoadNetns, "ip", "route", "add", ebpfLoadUnlistedAddr+"/32", "via", ebpfLoadHostAddr)
	t.Cleanup(func() {
		_ = exec.Command("ip", "netns", "exec", ebpfLoadNetns, "ip", "route", "del", ebpfLoadUnlistedAddr+"/32").Run() // #nosec G204 -- fixed argv
		_ = exec.Command("ip", "addr", "del", ebpfLoadUnlistedAddr+"/32", "dev", ebpfLoadHostVeth).Run()               // #nosec G204 -- fixed argv
	})
	ln, err := net.Listen("tcp4", net.JoinHostPort(ebpfLoadUnlistedAddr, fmt.Sprint(ebpfLoadUnlistedPort)))
	if err != nil {
		t.Fatalf("listen on %s: %v", ebpfLoadUnlistedAddr, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go acceptAndClose(ln)

	// A raw TCP socket on the unlisted peer sees every TCP segment delivered
	// to it, including ones its TCP stack would reset.
