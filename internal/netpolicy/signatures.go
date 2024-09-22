package netpolicy

import (
	"fmt"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Tier 2 (#661) — curated cleartext exploit signatures shipped with the daemon.
//
// These are matched (best-effort) against the first window of an INBOUND TCP
// payload by the eBPF egress program to virtually-patch a vulnerable service in
// a container before the vendor fix ships. The set is deliberately small and
// high-confidence: each pattern is a literal byte string that is strong evidence
// of an exploit attempt and rare in legitimate cleartext traffic. Operator-
// managed signatures (PR-B) augment this built-in baseline.
//
// Constraints (mirror the BPF program): a pattern must be 1..32 bytes; up to 32
// signatures are scanned. IDs are stable and nonzero (echoed in the audit on a
// match) — never renumber a shipped signature.

// SigMaxLen mirrors the BPF program's per-pattern byte limit (netbpf.SigMaxLen).
// Duplicated here so this pure package validates operator input without
// importing netbpf; a divergence is caught by a cross-package test in server.
const SigMaxLen = 32

