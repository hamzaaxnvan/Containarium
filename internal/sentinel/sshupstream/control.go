package sshupstream

import (
	"fmt"
	"github.com/footprintai/containarium/internal/sentinel/sshcontrol"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/tg123/sshpiper/libplugin"
)

// Option configures the optional shared controller without altering child policy.
type Option func(*adapter)
