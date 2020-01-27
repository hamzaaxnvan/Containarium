//go:build embed_bpf

package netbpf

import _ "embed"

// bpfObject is the compiled netpolicy BPF object, baked into the binary at build
