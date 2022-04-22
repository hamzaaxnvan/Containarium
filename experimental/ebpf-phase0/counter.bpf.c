// Phase 0 — bridge-attach validation for the eBPF network
// isolation design (see docs/security/NETWORK-ISOLATION-DESIGN.md).
//
// THROWAWAY. The only purpose is to confirm:
//   1. A tc-bpf program can be attached to incusbr0 cleanly.
//   2. The program sees container-to-container packets.
//   3. Attach/detach doesn't disrupt existing Incus container networking.
//
// If those three hold, Phase A productionalizes; if any fails, the
// design needs to be revised before the user-facing work starts.
//
// Build (the multiarch -I lets clang's bpf target find <asm/types.h>,
// which linux/bpf.h pulls in; without it the build fails on stock Ubuntu):
