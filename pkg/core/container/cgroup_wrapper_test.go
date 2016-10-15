package container

import (
	"strings"
	"testing"
)

func TestCgroupWrapperScript_Podman(t *testing.T) {
	script := cgroupWrapperScript("/usr/bin/podman")

	// Shebang
	if !strings.HasPrefix(script, "#!/bin/bash\n") {
		t.Error("wrapper script must start with #!/bin/bash shebang")
	}

	// Real binary path
	if !strings.Contains(script, "REAL=/usr/bin/podman") {
		t.Error("wrapper script must set REAL to the real binary path")
	}

	// Memory cgroup read
	if !strings.Contains(script, "/sys/fs/cgroup/memory.max") {
		t.Error("wrapper script must read memory.max from cgroup v2")
	}

	// CPU cgroup read
	if !strings.Contains(script, "/sys/fs/cgroup/cpu.max") {
		t.Error("wrapper script must read cpu.max from cgroup v2")
	}

	// Flag detection for --memory
	if !strings.Contains(script, "--memory=*|--memory|-m=*|-m") {
		t.Error("wrapper script must detect existing --memory / -m flags")
	}

	// Flag detection for --cpus
	if !strings.Contains(script, "--cpus=*|--cpus") {
		t.Error("wrapper script must detect existing --cpus flag")
	}

	// exec passthrough
	if !strings.Contains(script, "exec $REAL") {
		t.Error("wrapper script must exec the real binary")
	}

	// Docker Compose v2 limitation comment
