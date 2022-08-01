package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// flakyInstaller fails IsInstalled with a publickey-style handshake error
// for the first failN calls, then succeeds — modeling a freshly-created
// box that isn't reachable via the sentinel until the keysync propagates
