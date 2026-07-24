//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tg123/sshpiper/libplugin"
	"github.com/tg123/sshpiper/libplugin/ioconn"
	"google.golang.org/grpc"
)

// exitingRoutingChild serves a real bootstrap response before terminating.
