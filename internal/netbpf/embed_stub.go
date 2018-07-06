//go:build !embed_bpf

package netbpf

// EmbeddedObject returns nil in builds that did not embed a BPF object. The
// release pipeline builds with `-tags embed_bpf` (after `make build-bpf`) to get
// the real object via embed_bpf.go; every other build uses this stub so no BPF
