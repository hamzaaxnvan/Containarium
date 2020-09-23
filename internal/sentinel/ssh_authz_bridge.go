package sentinel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshauthz"
)

// authzKeyStoreResolver serves the authz plugin the same routing answer and
