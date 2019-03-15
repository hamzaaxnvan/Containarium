// Phase A — per-container-veth network-policy program for the eBPF
// network isolation design (see docs/security/NETWORK-ISOLATION-DESIGN.md).
//
// Attach point: each container's host-side veth, TC_INGRESS. Per the Phase 0
// findings, the veth ingress hook sees the container's ENTIRE outbound — the
// sender side of every flow — which is the only reliable observation/enforcement
// point for bridge-forwarded inter-container traffic.
//
// Phase A is OBSERVATION ONLY (log_only): the program evaluates each flow
// against the sender's tenant policy and, for flows that WOULD be denied,
// emits a perf event (and bumps a would-deny counter) — but always returns
// TC_ACT_OK. Nothing is dropped. Phase B flips would-deny to TC_ACT_SHOT when
// the per-veth mode is ENFORCE.
//
// The sender hook judges what a box OPENS. Its answers to flows that peers
// opened toward it are recognised through the inbound_flows map, which the
// egress hook (packets toward the box) seeds from validated sources (#2618);
// a box may only send with its own address (source validation, same issue).
//
// Build (the multiarch -I lets clang's bpf target find <asm/types.h>):
//   clang -O2 -g -target bpf -I/usr/include/$(uname -m)-linux-gnu \
//       -c netpolicy.bpf.c -o netpolicy.bpf.o
//
// Requires kernel ≥ 5.4 (LPM_TRIE + perf_event_output). Ubuntu 24.04 is fine.

#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/in.h>
#include <linux/tcp.h>
#include <linux/udp.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// Mode values — mirror pb.NetworkPolicyMode (config.proto). Phase A only ever
// loads LOG_ONLY here; ENFORCE is wired but the drop path is Phase B.
#define MODE_LOG_ONLY 1
#define MODE_ENFORCE  2

// Deny reasons carried in deny_event.reason — mirror netbpf.DenyReason* in Go.
// An older loader that reads only the first 19 bytes ignores it; the byte reuses
// the struct's former pad, so the wire size is unchanged.
#define DENY_REASON_POLICY        0  // failed allow-list / intra-tenant / metadata
#define DENY_REASON_VIRTUAL_PATCH 1  // matched an explicit virtual-patch deny rule (#660)
#define DENY_REASON_SIGNATURE     2  // matched a cleartext exploit signature (#661, Tier 2)
#define DENY_REASON_SPOOFED_SOURCE 3 // the box sent with a source address that is not its own (#2618)

// Verdict carried in deny_event.verdict — mirror netbpf.DenyVerdict* in Go. The
// verdict the program actually applied: with a separate egress_mode an
// allow-list miss can be logged while the tenant's mode is ENFORCE (and the
// reverse), so the consumer must not infer it from the tenant-wide mode. 0 is
// what an older object leaves in the former pad, so it means "not reported".
#define DENY_VERDICT_UNKNOWN 0
#define DENY_VERDICT_LOGGED  1
#define DENY_VERDICT_DROPPED 2

// Per-veth policy config, keyed by the veth's host ifindex. The loader writes
// one entry per managed container veth.
struct policy_cfg {
    __u32 tenant_id;     // the owning tenant (sender side)
    __u8  mode;          // MODE_LOG_ONLY | MODE_ENFORCE
    __u8  allow_intra;   // 1 = same-tenant container↔container allowed
    __u8  allow_metadata; // 1 = may reach 169.254.169.254 (default 0 = denied)
    __u8  egress_mode;   // mode of the egress allow-list decision ONLY; 0 = follow
                         // `mode` (the former pad byte, so an older loader that
                         // writes 0 here keeps today's behaviour). `mode` keeps
                         // governing the cross-tenant, intra-tenant, metadata,
                         // virtual-patch and signature checks.
    __u32 box_ip;        // the box's own IPv4, network byte order (#2618), from a
                         // HOST-authoritative source (the NIC's ipv4.address or the
                         // bridge's DHCP lease), never the guest's own report.
                         // APPENDED: an object built before it has an 8-byte value
                         // and the loader fits the value to the map. 0 = unknown:
                         // this veth validates nothing, so frames from it never seed
                         // an inbound flow (they are SEEN but not VALIDATED).
    __u8  flags;         // POLICY_FLAG_* (#2618). APPENDED after box_ip.
    __u8  pad[3];
};

// POLICY_FLAG_VALIDATE_ONLY: the veth belongs to an instance the daemon does not
// police (the control plane, an instance with no tenant). Its sender hook only
// marks frames (SEEN, and VALIDATED when the source is its box_ip) and passes —
// no accounting, no policy, no events, never a drop. Every Incus instance on the
// bridge gets at least this entry, so a frame WITHOUT the SEEN mark can only
// have come from the host itself or from outside the bridge.
#define POLICY_FLAG_VALIDATE_ONLY 1

// skb->mark bits the sender hook sets on every frame from a managed veth (#2618).
// The egress hook reads them to decide whether a frame may seed an inbound flow:
// SEEN without VALIDATED is a container frame whose source did not match its
// veth's box_ip (a forgery, or an unknown address) and is never trusted —
// whatever that container's mode, and however the frame reached the box (bridged
// or routed through the host). A frame with neither bit did not pass any managed
// veth's hook: the host's own traffic, or traffic routed in from outside.
//
// A box cannot forge these bits: the mark is host-side skb metadata, and the
// kernel scrubs it when a packet crosses a netns (veth xmit). The two top bits
// are used so that a firewall mark an operator sets in the low bits is left
// alone; the bits are preserved across bridge forwarding and host routing within
// the host netns. Writing skb->mark from a TC classifier needs no particular
// kernel; the attach path (TCX) already needs 6.6.
#define NETPOLICY_MARK_SEEN      0x40000000u
#define NETPOLICY_MARK_VALIDATED 0x80000000u

// Cloud metadata service IP (169.254.169.254). Compared against the packet's
// network-byte-order daddr via bpf_htonl. #315 Phase D.
#define METADATA_IPV4 0xA9FEA9FE

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);                 // host veth ifindex
    __type(value, struct policy_cfg);
    __uint(max_entries, 4096);
} veth_policy SEC(".maps");

// Egress allow-list as an LPM trie, scoped per tenant: the key carries the
// tenant_id in its high bits followed by the destination prefix, so a lookup
// only matches CIDRs the sender's tenant is allowed to reach. prefixlen counts
// the full tenant_id (32 bits) + the IPv4 prefix bits.
struct egress_key {
    __u32 prefixlen;     // 32 + cidr_bits
    __u32 tenant_id;     // big-endian-irrelevant: exact-matched, 32 prefix bits
    __u32 addr;          // network byte order, masked to cidr_bits
};

struct {
    __uint(type, BPF_MAP_TYPE_LPM_TRIE);
    __type(key, struct egress_key);
    __type(value, __u8);
    __uint(max_entries, 65536);
    __uint(map_flags, BPF_F_NO_PREALLOC);
} egress_cidr SEC(".maps");

// Virtual-patch deny rules (#660). Same tenant-scoped LPM key as egress_cidr,
// but a richer value so a rule can be scoped to a destination port/proto: a CVE
// in a service on a known port can be blocked without blackholing the whole
// host. Evaluated BEFORE the allow logic — deny beats allow. The LPM key is
// CIDR-only (port/proto live in the value), so there is at most ONE deny entry
// per (tenant, CIDR); to block two ports on the same host, deny the host
// outright (port 0 = any) — a documented Tier-1 limitation.
struct deny_val {
    __u16 port;   // host byte order; 0 = any port
    __u8  proto;  // IP protocol number; 0 = any proto
    __u8  flags;  // reserved (0)
};

struct {
    __uint(type, BPF_MAP_TYPE_LPM_TRIE);
    __type(key, struct egress_key);
    __type(value, struct deny_val);
    __uint(max_entries, 65536);
    __uint(map_flags, BPF_F_NO_PREALLOC);
} deny_cidr SEC(".maps");

// Destination-IP → tenant_id for intra-backend traffic. The loader populates
// this from every managed container's IP, so the program can tell "dst is
// another container of tenant T" from "dst is external".
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);                 // dst IPv4 (network byte order)
    __type(value, __u32);               // tenant_id
    __uint(max_entries, 4096);
} ip_tenant SEC(".maps");

// Stats counters the validator reads (like Phase 0). 0=seen, 1=would_deny,
// 2=inbound_reseed (#2618: a TCP inbound-flow entry re-created from a validated
// MID-STREAM segment, i.e. after an LRU eviction or an idle expiry healed itself;
// a box cannot inflate it, since only inbound packets write the map — the
// daemon logs its growth each reconcile pass as the LRU-pressure signal).
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, __u32);
    __type(value, __u64);
    __uint(max_entries, 3);
} stats SEC(".maps");

#define STAT_SEEN           0
#define STAT_WOULD_DENY     1
#define STAT_INBOUND_RESEED 2

static __always_inline void bump(__u32 idx) {
    __u64 *v = bpf_map_lookup_elem(&stats, &idx);
    if (v)
        __sync_fetch_and_add(v, 1);
}

// Per-flow accounting (issue #627). Every observed flow on a managed veth gets a
// cumulative bytes/packets tally here, keyed by its 5-tuple, so the daemon can
// populate the traffic view (src/dst IP + byte counts) straight from eBPF —
// independent of conntrack accounting and the IP→container cache. Attribution is
// exact: the ifindex in the key maps 1:1 to a container. Recorded for ALLOWED and
// would-deny flows alike (it's usage accounting, not a policy decision).
//
// The veth ingress hook sees the container's outbound (sender) side, so the
// packets/bytes counts are the container's EGRESS. The reply direction
// (peer→container) is captured by a second program on the veth's TC_EGRESS hook
// (#631), tallied into rx_packets/rx_bytes on the SAME flow entry (the egress
// program rebuilds the request-oriented key by swapping the reply's tuple), so a
// flow carries both directions.
//
// LRU_HASH so a burst of short-lived flows self-evicts under pressure rather than
// filling the map; the daemon reads it on a poll and ages out idle entries.
struct flow_key {
    __u32 ifindex;   // host veth — attribution handle (1:1 with a container)
    __u32 saddr;     // source IPv4, network byte order
    __u32 daddr;     // destination IPv4, network byte order
    __u16 sport;     // host byte order
    __u16 dport;     // host byte order
    __u8  proto;     // IP protocol number
    __u8  pad[3];
};

// flow_stat: tx_* (packets/bytes) are the container's egress, observed on the
// veth ingress hook; rx_* are the reply direction, observed on the veth egress
// hook (#631). rx fields are APPENDED so an older 32-byte value (pre-#631) still
// decodes — the Go reader treats missing rx as 0.
struct flow_stat {
    __u64 packets;     // tx: container → peer
    __u64 bytes;
    __u64 first_ns;    // bpf_ktime_get_ns at first packet (monotonic)
    __u64 last_ns;     // bpf_ktime_get_ns at most recent packet (monotonic)
    __u64 rx_packets;  // reply: peer → container (#631)
    __u64 rx_bytes;
};

struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __type(key, struct flow_key);
    __type(value, struct flow_stat);
    __uint(max_entries, 65536);
} flows SEC(".maps");

// --- #2618: replies to connections opened TOWARD the box -----------------------
//
// The sender hook judges every packet a box sends and keeps no connection
// state, so a box's SYN-ACK, data or echo reply to a peer that connected IN is
// judged as if the box had opened a new flow — and dropped under enforce with
// an empty allow-list. An egress policy says what a box may OPEN, not whether
// it may answer; this map records the flows peers open toward the box so the
// sender hook can recognise the answers.
//
// Soundness rules (the naive "judge only a fresh SYN" is an exfil channel: a
// box is root in its netns and can send ACK-flagged segments with a payload to
// anyone, which #3070's forged-ACK guard pins):
//   - written ONLY on the egress hook (packets toward the box), so nothing the
//     box sends creates or keeps an entry; the trust is in the inbound
//     direction. A validated TCP segment of any shape, a UDP datagram or an ICMP
//     echo request creates or refreshes the entry: re-creating a TCP entry from
//     a mid-stream segment is exactly as sound as from a SYN (the same peer
//     could send a SYN), and it makes an LRU eviction self-healing — the peer's
//     next segment restores the entry instead of the connection dying;
//   - ONLY when the packet's source is validated, which the frame's skb->mark
//     says (NETPOLICY_MARK_*): a frame that passed a managed veth's sender hook
//     carries SEEN, and VALIDATED only when its source was that veth's own
//     box_ip. SEEN without VALIDATED is never trusted — a neighbour cannot forge
//     a SYN "from" an external address P and so open the target's egress to P,
//     whatever the neighbour's own mode (its hook drops the forgery only under
//     enforce, but it always withholds VALIDATED) and whether the frame was
//     bridged or routed through the host. A frame with neither bit is trusted
//     ONLY if it is the host's own (ingress_ifindex 0) or came in on a known
//     uplink (trusted_iif); every other unmarked frame fails closed;
//   - consulted on the sender hook AFTER the virtual-patch and metadata
//     checks (both still win, fail closed), with a reply-only shape per
//     protocol (see inbound_reply) and a per-protocol idle timeout;
//   - a packet with IP options or any fragmentation is neither a seed nor a
//     reply (its "ports" would be sender-chosen bytes); it falls through to the
//     ordinary checks.
struct inbound_key {
    __u32 ifindex;    // the box's host veth (1:1 with a box)
    __u32 box_ip;     // network byte order
    __u32 peer_ip;    // network byte order
    __u16 box_port;   // host byte order; 0 for ICMP
    __u16 peer_port;  // host byte order; the echo id for ICMP
    __u8  proto;
    __u8  pad[3];
};

struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __type(key, struct inbound_key);
    __type(value, __u64);               // last inbound packet, bpf_ktime_get_ns
    __uint(max_entries, 65536);
} inbound_flows SEC(".maps");

// Host interfaces whose UNMARKED frames may seed an inbound flow (#2618): the
// host's uplinks (default-route interfaces), written by the daemon. An unmarked
// frame with ingress_ifindex 0 is the host's own; one from an interface in this
// map was routed in from outside the bridge, where the host's rp_filter and the
// cloud firewall are the source guards. Any other unmarked frame — a veth the
// daemon has not configured yet (a new instance before its first reconcile), a
// non-Incus namespace attached to the bridge, or a frame whose mark something
// rewrote — is NOT trusted (fail closed).
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);                 // host ifindex
    __type(value, __u8);
    __uint(max_entries, 64);
} trusted_iif SEC(".maps");

// Idle timeouts, checked on lookup. Inbound packets alone refresh an entry, so a
// box can only keep answering a peer while that peer keeps talking to it: on a
// TCP connection the peer's ACKs do that for as long as the box sends. Because
// any validated inbound TCP segment re-creates a lapsed entry, an idle shell
// still works when the peer types again; what a lapse costs is only a server
// push after a silence longer than this. 4h keeps stale residency in the
// shared LRU short. UDP and ICMP echo follow conntrack's order of magnitude.
#define INBOUND_IDLE_TCP_NS  (14400ULL * 1000000000ULL) // 4h
#define INBOUND_IDLE_UDP_NS  (120ULL * 1000000000ULL)   // 120s
#define INBOUND_IDLE_ICMP_NS (30ULL * 1000000000ULL)    // 30s

static __always_inline __u64 inbound_idle_ns(__u8 proto) {
    if (proto == IPPROTO_TCP)
        return INBOUND_IDLE_TCP_NS;
    if (proto == IPPROTO_UDP)
        return INBOUND_IDLE_UDP_NS;
    return INBOUND_IDLE_ICMP_NS;
}

// The ICMP echo header. <linux/icmp.h> is not usable from the bpf target (it
// pulls <linux/if.h> and with it glibc's <sys/socket.h>), so the 8 header bytes
// the program needs are declared here; the layout is RFC 792's.
struct icmp_echo_hdr {
    __u8   type;
    __u8   code;
    __be16 checksum;
    __be16 id;
    __be16 sequence;
};
#define ICMP_ECHOREPLY 0
#define ICMP_ECHO      8

// l4_info is the L4 header both hooks parse once. ok=0 means the L4 header is
// not where a plain packet keeps it — the IP header carries options (ihl != 5),
// the packet is a fragment (MF set or a non-zero offset), or the header is not
// in the packet. Such a packet never counts as a reply and never seeds a flow
// (its bytes at the port offsets are whatever the sender put there); ports stay
// 0 and it falls through to the ordinary checks. The virtual-patch deny and the
// flow accounting share the same guard (#3166): a port-scoped deny treats an
// unknown TCP/UDP port as matching (fails closed), and the accounting skips
// fragments and tallies an options-bearing packet under ports 0.
struct l4_info {
    __u16 sport;     // host byte order (TCP/UDP), else 0
    __u16 dport;
    __u16 icmp_id;   // host byte order, echo request/reply only
    __u8  ok;
    __u8  tcp_syn;
    __u8  tcp_ack;
    __u8  icmp_type;
    __u8  icmp_echo; // 1 = type is echo request or echo reply (icmp_id valid)
};

// IP_FRAG_MASK covers the MF flag and the 13-bit fragment offset (DF is fine).
#define IP_FRAG_MASK 0x3FFF

static __always_inline int ip_is_fragment(const struct iphdr *ip) {
    return (bpf_ntohs(ip->frag_off) & IP_FRAG_MASK) != 0;
}

static __always_inline void parse_l4(struct iphdr *ip, void *data_end, struct l4_info *l4) {
    if (ip->ihl != 5 || ip_is_fragment(ip))
        return; // options or a fragment: not parsed, fails closed
    if (ip->protocol == IPPROTO_TCP) {
        struct tcphdr *tcp = (void *)ip + sizeof(*ip);
        if ((void *)(tcp + 1) <= data_end) {
            l4->ok = 1;
            l4->sport = bpf_ntohs(tcp->source);
            l4->dport = bpf_ntohs(tcp->dest);
            l4->tcp_syn = tcp->syn;
            l4->tcp_ack = tcp->ack;
        }
    } else if (ip->protocol == IPPROTO_UDP) {
        struct udphdr *udp = (void *)ip + sizeof(*ip);
        if ((void *)(udp + 1) <= data_end) {
            l4->ok = 1;
            l4->sport = bpf_ntohs(udp->source);
            l4->dport = bpf_ntohs(udp->dest);
        }
    } else if (ip->protocol == IPPROTO_ICMP) {
        struct icmp_echo_hdr *icmp = (void *)ip + sizeof(*ip);
        if ((void *)(icmp + 1) <= data_end) {
            l4->ok = 1;
            l4->icmp_type = icmp->type;
            if (icmp->type == ICMP_ECHO || icmp->type == ICMP_ECHOREPLY) {
                l4->icmp_echo = 1;
                l4->icmp_id = bpf_ntohs(icmp->id);
            }
        }
    }
}
