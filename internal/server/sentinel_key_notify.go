package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/sentinel"
)

// Event-driven SSH key propagation (cloud #971).
//
// A box's host-side authorized_keys entry only becomes usable once the
// sentinel has pulled it into sshpiper's routing table. That pull runs on a
// 2-minute ticker, so a box created just after a tick reports RUNNING while
// SSH still answers `Permission denied (publickey)` — for up to two minutes,
// ~50s in the field. Every caller that trusts RUNNING (CI, agents, the
// create→ssh flow in the docs) fails its first connections.
//
// The daemon therefore tells the sentinel the moment its key set changes.
// The ticker stays exactly as it was: this is an accelerator, not a
