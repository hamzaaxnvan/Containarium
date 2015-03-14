package container

import (
	"fmt"
	"strings"

	incusapi "github.com/lxc/incus/v7/shared/api"

	"github.com/footprintai/containarium/pkg/core/ostype"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// resolveInstanceType decides whether a create produces an LXC container
// or a QEMU/KVM VM from the (os_type, isolation) pair (#2196).
//
// UNSPECIFIED isolation is exactly the pre-#2196 rule — Windows is a VM,
// everything else a container — so existing callers are byte-for-byte
// unaffected. An explicit VM is honored for any OS; an explicit CONTAINER
// is refused for Windows, which has no container image.
