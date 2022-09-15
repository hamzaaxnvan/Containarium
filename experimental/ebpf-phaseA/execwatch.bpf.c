// execwatch — host-side process-exec telemetry for the process guardrail
// (#2637; design: docs/architecture/process-exec-guardrail.md, component 1).
//
// A SECOND BPF object next to netpolicy.bpf.o, deliberately not merged into
// it: this program is host-global (one tracepoint) where the network program
// is per-veth, it needs a newer kernel (see the floor below) and kernel BTF,
// and a verifier rejection or missing BTF here must never take flow
// enforcement down with it. Same `make build-bpf`, same `embed_bpf` tag,
// same CI loading lane; only the failure domain is split.
//
// Attach point: tracepoint sched/sched_process_exec — fires once per
// SUCCESSFUL exec (after the new image is installed), with the resolved
// filename. Not sys_enter_execve: that records attempts and would need
// sys_exit pairing to know the exec happened.
//
// Filtering is in-kernel: userspace fills `watched_cgroups` with the cgroup
// id (the inode of /sys/fs/cgroup/<path>) of every running tenant container,
// resolved from the container's init PID. The program walks the exec'ing
// task's cgroup ancestors (levels 1..EXEC_CGROUP_LEVELS) and looks each id
// up; a miss at every level is a host or core-infra process and nothing is
// emitted — the ring only ever carries tenant-box execs. The matched
// ancestor id (the one userspace registered) is what the event reports, so
// userspace maps it straight back to the container.
//
// argv is read from the task's mm (arg_start..arg_end) via CO-RE, capped at
// EXEC_ARGV_LEN bytes with a truncation flag. The environment block
// (env_start..env_end) is NEVER read: the read window is bounded by arg_end.
//
// Build: `make build-bpf` generates vmlinux.h from the build host's kernel
// BTF (`bpftool btf dump file /sys/kernel/btf/vmlinux format c`) into
// internal/netbpf/ — gitignored, never checked in (decided on #2711 Q1) —
// then runs:
//   clang -O2 -g -target bpfel -I internal/netbpf -c execwatch.bpf.c -o execwatch.bpf.o
//
// Kernel floor: 5.8 (BPF_MAP_TYPE_RINGBUF, and with it the BPF_RB_NO_WAKEUP
// submit flag), 5.6 (bpf_get_current_ancestor_cgroup_id),
// and CONFIG_DEBUG_INFO_BTF for the CO-RE relocations (task_struct, mm_struct,
// the tracepoint context). Ubuntu 22.04 and 24.04 LTS ship all three. The
// network object keeps its own 5.4 floor: a host below 5.8 loses exec
// telemetry, not flow detection.

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_core_read.h>

#define EXEC_COMM_LEN      16
#define EXEC_FILENAME_LEN  256
#define EXEC_ARGV_LEN      512
#define EXEC_CGROUP_LEVELS 12
// 4 MiB (#2774 review): with the ring drained every 50 ms rather than per
// record, the ring must absorb one interval of every watched box's execs.
// 4 MiB holds ~5,090 records (816 bytes + the 8-byte ring header), i.e. a
// host-wide ~100,000 execs/s for one interval before anything is lost. The
// Go side pins this as execRingBytes.
#define EXEC_RING_BYTES    (4 << 20) /* 4 MiB */

