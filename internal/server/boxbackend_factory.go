//go:build !windows

package server

import (
	"context"
	"fmt"
	"log"

	"github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/internal/controller"
	"github.com/footprintai/containarium/pkg/core/box"
	boxk8s "github.com/footprintai/containarium/pkg/core/box/k8s"
	boxlxc "github.com/footprintai/containarium/pkg/core/box/lxc"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// RuntimeLXC and RuntimeK8s are the accepted values for CONTAINARIUM_RUNTIME /
// --runtime. LXC is the default for backward compatibility.
const (
	RuntimeLXC = "lxc"
	RuntimeK8s = "k8s"
)
