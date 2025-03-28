#!/bin/bash
#
# Density loop for the third scenario: create Containarium boxes one at a
# time on the native LXC/Incus backend (no k8s, no gVisor) until the
# shared stopping rule in lib.sh's run_density_loop triggers.
#
# Unlike the k8s backend (04-run-density-containarium.sh), `containarium
# list` works fine here — #1525's "incus backend not available" only
# fires against a --runtime=k8s daemon; this one genuinely is Incus-backed.
#
# CPU/memory format differs from the k8s scripts too: pkg/core/incus's
# parseCPULimit accepts Kubernetes millicpu directly (confirmed in code:
# "250m" -> limits.cpu + limits.cpu.allowance), but memory is passed
# straight through to Incus's own `limits.memory` config key with no
# conversion (pkg/core/container/manager.go) — Incus's native format is
# "256MiB", not Kubernetes' "256Mi" (confirmed against this repo's own
# test fixtures, e.g. pkg/core/nodevm/nodevm_test.go's "1GiB"). Uses
# SANDBOX_CPU_LIMIT/SANDBOX_MEM_LIMIT the same way 04 does (LXC has one
# enforced ceiling, not a request+limit pair), converting Mi->MiB (a
# rename, not a real unit conversion — MiB *is* what k8s' "Mi" means).
#
# `--podman=false`: found live that `create`'s default (--podman=true)
# installs Podman + pip + podman-compose inside every box
# (pkg/core/container/manager.go's installPackages) — under this
# benchmark's tiny 50m CPU limit, that single install chain took several
# minutes per unit (watched it firsthand: still running `apt-get install
# python3-pip` six minutes in). That's not a density measurement, it's a
# package-manager-under-extreme-CPU-starvation measurement, and neither
# the control group's busybox pods nor the k8s-backend's agent-box image
# do anything comparable at create time. Disabling it drops the per-unit cost to just the base package set
# (openssh-server, sudo, curl, git, vim, htop, net-tools, iputils-ping —
# pkg/core/ospkg/debian.go, still installed via apt at create time either
# way). That's still not perfectly comparable to the other two scenarios
# — neither installs anything at create time at all (bare busybox for
# the control group; a pre-baked agent-box image for the k8s-backend
# Containarium group) — but it's the closest this backend gets without
# baking its own base image, and it's the real difference worth reporting
# rather than a multi-minute artifact worth reporting.
#
# Usage:
#   scripts/06-run-density-containarium-lxc.sh --name <vm-name> [--start-index N]
#
# --start-index: resume numbering after a prior run stopped for a reason
# that wasn't actually resource exhaustion (e.g. #1532 — `containarium
# list` itself was timing out past ~180 containers on one host, not the
# host running out of memory/CPU/disk; once that's fixed, re-running from
# scratch would just re-create the same 180 units for no reason). Existing
# units below start-index are left alone — this only affects where the
# loop's own counter begins.

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=lib.sh
source ./lib.sh

