package container

import (
	"testing"

	incusapi "github.com/lxc/incus/v7/shared/api"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestResolveInstanceType pins the (os_type, isolation) → Incus instance
// type table from docs/architecture/ssh-new-anonymous-box.md: UNSPECIFIED
// keeps the pre-#2196 behavior (Windows → VM, else container), VM is
