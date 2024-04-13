//go:build !windows && !containarium_client

package cmd

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tg123/sshpiper/libplugin"

	"github.com/footprintai/containarium/internal/sentinel"
	"github.com/footprintai/containarium/internal/sentinel/anondoor"
	"github.com/footprintai/containarium/internal/sentinel/sshcontrol"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var (
