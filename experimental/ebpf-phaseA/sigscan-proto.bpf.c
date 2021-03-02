// Tier 2 (#661) VERIFIER PROTOTYPE — bounded cleartext signature scanning.
//
// Standalone and NOT loaded by the daemon. Its only job is to prove the kernel
// verifier accepts the risky core of Tier 2 — copying a payload window and
// running a bounded substring search against a signature map — before we wire it
// into the production netpolicy.bpf.c. eBPF can't run on the dev mac, so this is
// validated on a Linux backend:
//
//   clang -O2 -g -target bpfel -I/usr/include/$(uname -m)-linux-gnu \
//       -c sigscan-proto.bpf.c -o /tmp/sigscan.o
//   sudo bpftool prog loadall /tmp/sigscan.o /sys/fs/bpf/sigscan type tc
//   # LOADED OK => the verifier accepts the scan; clean up: sudo rm -rf /sys/fs/bpf/sigscan
//
// Design notes from three rejected attempts (kept so the next person doesn't
// repeat them):
//   - Naive nested loops with a variable inner bound  -> "infinite loop detected"
//     (clang -O2 makes a back-edge the verifier can't prove terminates).
//   - Fully unrolling the offset+pattern loops        -> still "infinite loop" on
//     the signature counter AND a 600 KB object. Unrolling is a dead end here.
//   - WORKS: a single bpf_loop() over the flattened (signature x offset) space,
//     with only the tiny pattern-compare loop unrolled. bpf_loop is verified
//     ONCE regardless of trip count, so there is no back-edge to mis-analyze. The
//     payload window lives in a PER-CPU scratch map (not the stack), re-looked-up
//     inside the callback, so the verifier always has a clean map-value pointer
//     with a known bound — passing a stack pointer through the callback ctx loses
//     that bound.

#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/in.h>
#include <linux/tcp.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// Tunables. SCAN_WINDOW must be a power of two (index masking). bpf_loop runs
// SIG_MAX_COUNT * SCAN_WINDOW iterations worst case (8 * 64 = 512).
#define SIG_MAX_LEN   32
#define SIG_MAX_COUNT 32
#define SCAN_WINDOW   256

struct signature {
    __u8  len;                 // active bytes in pat, 1..SIG_MAX_LEN (0 = empty)
    __u8  enabled;             // 1 = scan this slot
    __u16 id;                  // signature id (>0), echoed on a match
    __u8  pat[SIG_MAX_LEN];
};

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, __u32);
    __type(value, struct signature);
    __uint(max_entries, SIG_MAX_COUNT);
} signatures SEC(".maps");

// Per-CPU scratch for the copied payload window. Per-CPU so concurrent packets on
